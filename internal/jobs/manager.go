package jobs

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	appcfg "main.go/internal/appconfig"
	"main.go/internal/deploy"
	gh "main.go/internal/github"
)

type Manager struct {
	mu       sync.Mutex
	accounts []gh.Account
	deploy   *deploy.Service
	cfg      appcfg.JobConfig
	jobs     []Job
	nextID   int64
}

type StartRequest struct {
	Concurrency    int               `json:"concurrency"`
	RunMinutes     int               `json:"run_minutes"`
	WorkerCount    int               `json:"worker_count"`
	AccountPrefix  string            `json:"account_prefix"`
	PasswordPrefix string            `json:"password_prefix"`
	TargetCount    int               `json:"target_count"`
	MaxRetries     int               `json:"max_retries"`
	AutoDeploy     bool              `json:"auto_deploy"`
	Inputs         map[string]string `json:"inputs"`
}

type Job struct {
	ID          int64             `json:"id"`
	AccountName string            `json:"account_name"`
	Owner       string            `json:"owner"`
	Repo        string            `json:"repo"`
	WorkflowID  string            `json:"workflow_id"`
	Status      string            `json:"status"`
	Message     string            `json:"message"`
	RunID       int64             `json:"run_id,omitempty"`
	RunURL      string            `json:"run_url,omitempty"`
	RetryCount  int               `json:"retry_count"`
	MaxRetries  int               `json:"max_retries"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func NewManager(accounts []gh.Account, deploySvc *deploy.Service, cfg appcfg.JobConfig) *Manager {
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = 3
	}
	if cfg.PollIntervalSecs == 0 {
		cfg.PollIntervalSecs = 20
	}
	return &Manager{accounts: accounts, deploy: deploySvc, cfg: cfg, nextID: 1}
}

func (m *Manager) SetAccounts(accounts []gh.Account) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts = accounts
}

// SetPoolAccounts 由 Web 账号池同步
func (m *Manager) SetPoolAccounts(accounts []gh.Account) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts = gh.EnabledAccounts(accounts)
}

func (m *Manager) StartBatch(ctx context.Context, req StartRequest) ([]Job, error) {
	accounts := m.enabledAccounts()
	if req.Concurrency <= 0 {
		req.Concurrency = 1
	}
	if req.RunMinutes <= 0 {
		req.RunMinutes = 20
	}
	if req.WorkerCount <= 0 {
		req.WorkerCount = 1
	}
	if req.AccountPrefix == "" {
		req.AccountPrefix = "User"
	}
	if req.PasswordPrefix == "" {
		req.PasswordPrefix = "Pass"
	}
	if req.MaxRetries <= 0 {
		req.MaxRetries = m.cfg.MaxRetries
	}
	if len(accounts) == 0 {
		return nil, errors.New("没有可用 GitHub 账号")
	}
	if req.Concurrency > len(accounts) {
		req.Concurrency = len(accounts)
	}
	started := make([]Job, 0, req.Concurrency)
	for i := 0; i < req.Concurrency; i++ {
		job := m.StartOne(ctx, accounts[i], req)
		started = append(started, job)
	}
	return started, nil
}

func (m *Manager) StartOne(ctx context.Context, account gh.Account, req StartRequest) Job {
	inputs := map[string]string{
		"run_minutes":     strconv.Itoa(req.RunMinutes),
		"worker_count":    strconv.Itoa(req.WorkerCount),
		"account_prefix":  req.AccountPrefix,
		"password_prefix": req.PasswordPrefix,
		"target_count":    strconv.Itoa(req.TargetCount),
	}
	for k, v := range req.Inputs {
		inputs[k] = v
	}
	job := Job{ID: m.nextJobID(), AccountName: account.Name, Owner: account.Owner, Repo: account.Repo, WorkflowID: account.WorkflowID, Status: "created", Inputs: inputs, MaxRetries: req.MaxRetries, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	m.addJob(job)
	if req.AutoDeploy && m.deploy != nil {
		m.updateJob(job.ID, func(j *Job) { j.Status = "deploying"; j.Message = "正在准备仓库和源码" })
		if err := m.deploy.Prepare(ctx, account); err != nil {
			m.updateJob(job.ID, func(j *Job) { j.Status = "failed"; j.Message = err.Error() })
			return m.mustJob(job.ID)
		}
	}
	m.dispatch(ctx, job.ID, account, inputs)
	return m.mustJob(job.ID)
}

func (m *Manager) StartPoller(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(m.cfg.PollIntervalSecs) * time.Second)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Poll(ctx)
			}
		}
	}()
}

func (m *Manager) Poll(ctx context.Context) {
	jobs := m.Jobs()
	for _, job := range jobs {
		if job.RunID == 0 || isTerminal(job.Status) {
			continue
		}
		account, ok := m.accountFor(job)
		if !ok {
			continue
		}
		run, err := gh.NewClient(account).GetRun(ctx, job.RunID)
		if err != nil {
			m.updateJob(job.ID, func(j *Job) { j.Message = err.Error() })
			continue
		}
		m.updateJob(job.ID, func(j *Job) {
			j.Status = run.Status
			j.RunURL = run.HTMLURL
			if run.Status == "completed" {
				if run.Conclusion == "success" {
					j.Status = "success"
					j.Message = "GitHub Actions VM 执行成功"
				} else if run.Conclusion == "cancelled" {
					j.Status = "cancelled"
					j.Message = "VM 运行超时取消，可能触发限流"
				} else {
					j.Status = "failed"
					j.Message = "GitHub Actions VM 失败: " + run.Conclusion
				}
			}
		})
		latest := m.mustJob(job.ID)
		// 失败或取消时自动切换到下一个账号
		if (latest.Status == "failed" || latest.Status == "cancelled") && latest.RetryCount < latest.MaxRetries {
			m.RetryFailed(ctx, latest.ID)
		}
	}
}

func (m *Manager) RetryFailed(ctx context.Context, jobID int64) error {
	job, ok := m.Job(jobID)
	if !ok {
		return nil
	}
	accounts := m.enabledAccounts()
	if len(accounts) == 0 {
		return nil
	}
	account := accounts[(job.RetryCount+1)%len(accounts)]
	m.updateJob(job.ID, func(j *Job) {
		j.Status = "retrying"
		j.RetryCount++
		j.AccountName = account.Name
		j.Owner = account.Owner
		j.Repo = account.Repo
		j.WorkflowID = account.WorkflowID
		j.RunID = 0
		j.RunURL = ""
		j.Message = "正在切换 GitHub VM"
	})
	m.dispatch(ctx, job.ID, account, job.Inputs)
	return nil
}

func (m *Manager) Jobs() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, len(m.jobs))
	copy(out, m.jobs)
	return out
}

func (m *Manager) Job(id int64) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, job := range m.jobs {
		if job.ID == id {
			return job, true
		}
	}
	return Job{}, false
}

func (m *Manager) dispatch(ctx context.Context, jobID int64, account gh.Account, inputs map[string]string) {
	client := gh.NewClient(account)
	if err := client.DispatchWorkflow(ctx, "main", inputs); err != nil {
		m.updateJob(jobID, func(j *Job) { j.Status = "failed"; j.Message = err.Error() })
		return
	}
	m.updateJob(jobID, func(j *Job) { j.Status = "dispatched"; j.Message = "GitHub Actions VM 已触发" })
	time.Sleep(2 * time.Second)
	runs, err := client.ListRuns(ctx, 5)
	if err != nil || len(runs) == 0 {
		return
	}
	run := runs[0]
	m.updateJob(jobID, func(j *Job) {
		j.RunID = run.ID
		j.RunURL = run.HTMLURL
		j.Status = run.Status
	})
}

func (m *Manager) enabledAccounts() []gh.Account {
	m.mu.Lock()
	defer m.mu.Unlock()
	return gh.EnabledAccounts(m.accounts)
}

func (m *Manager) accountFor(job Job) (gh.Account, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, account := range m.accounts {
		if account.Name == job.AccountName && account.Owner == job.Owner && account.Repo == job.Repo {
			return account, true
		}
	}
	return gh.Account{}, false
}

func (m *Manager) nextJobID() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.nextID
	m.nextID++
	return id
}

func (m *Manager) addJob(job Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append([]Job{job}, m.jobs...)
}

func (m *Manager) updateJob(id int64, fn func(*Job)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.jobs {
		if m.jobs[i].ID == id {
			fn(&m.jobs[i])
			m.jobs[i].UpdatedAt = time.Now()
			return
		}
	}
}

func (m *Manager) mustJob(id int64) Job {
	job, _ := m.Job(id)
	return job
}

func isTerminal(status string) bool {
	return status == "success" || status == "exhausted"
}
