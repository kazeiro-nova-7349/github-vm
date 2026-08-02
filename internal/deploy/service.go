package deploy

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	appcfg "main.go/internal/appconfig"
	gh "main.go/internal/github"
)

type Service struct {
	Config appcfg.DeployConfig
}

func NewService(cfg appcfg.DeployConfig) *Service {
	if cfg.RepoPath == "" {
		cfg.RepoPath = "."
	}
	if cfg.Remote == "" {
		cfg.Remote = "auto-vm"
	}
	return &Service{Config: cfg}
}

func (s *Service) Prepare(ctx context.Context, account gh.Account) error {
	if err := s.EnsureRepository(ctx, account); err != nil {
		return err
	}
	if err := s.EnsureWorkflow(ctx); err != nil {
		return err
	}
	return s.PushSource(ctx, account)
}

func (s *Service) EnsureRepository(ctx context.Context, account gh.Account) error {
	return gh.NewClient(account).EnsureRepo(ctx, s.Config.Private)
}

func (s *Service) EnsureWorkflow(ctx context.Context) error {
	return EnsureWorkflowFile(s.Config.RepoPath)
}

func (s *Service) PushSource(ctx context.Context, account gh.Account) error {
	if err := s.git(ctx, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("当前目录不是 git 仓库: %w", err)
	}
	remoteURL := fmt.Sprintf("https://x-access-token:%s@github.com/%s/%s.git", account.Token, account.Owner, account.Repo)
	if err := s.git(ctx, "remote", "get-url", s.Config.Remote); err != nil {
		if err := s.git(ctx, "remote", "add", s.Config.Remote, remoteURL); err != nil {
			return err
		}
	} else if err := s.git(ctx, "remote", "set-url", s.Config.Remote, remoteURL); err != nil {
		return err
	}
	for _, path := range []string{".gitignore", ".github/workflows/register.yml", "cmd/web", "internal", "wanzheng", "go.mod", "go.sum", "main.go", "config.json", "README.md", "4399ocr", "sfz.txt"} {
		if err := s.git(ctx, "add", path); err != nil {
			return err
		}
	}
	if hasDiff, err := s.hasStagedDiff(ctx); err != nil {
		return err
	} else if hasDiff {
		if err := s.git(ctx, "commit", "-m", "auto: update vm runner files"); err != nil {
			return err
		}
	}
	return s.git(ctx, "push", s.Config.Remote, "HEAD:main")
}

func (s *Service) hasStagedDiff(ctx context.Context) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--cached", "--quiet")
	cmd.Dir = s.Config.RepoPath
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func (s *Service) git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = s.Config.RepoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
