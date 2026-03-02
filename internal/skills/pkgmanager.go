package skills

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// PackageInfo describes a package found via search.
type PackageInfo struct {
	Name        string
	Version     string
	Description string
	Source      string
}

// PackageManager abstracts system package installation and search.
type PackageManager interface {
	Name() string
	Install(ctx context.Context, pkg string) error
	IsInstalled(pkg string) bool
	Search(pkg string) ([]PackageInfo, error)
}

// ManualManager is the fallback when no system package manager is detected.
type ManualManager struct{}

func (m *ManualManager) Name() string { return "manual" }
func (m *ManualManager) Install(_ context.Context, pkg string) error {
	return fmt.Errorf("no package manager detected; install %s manually", pkg)
}
func (m *ManualManager) IsInstalled(pkg string) bool {
	_, err := exec.LookPath(pkg)
	return err == nil
}
func (m *ManualManager) Search(_ string) ([]PackageInfo, error) {
	return nil, nil
}

// BrewManager manages packages via Homebrew.
type BrewManager struct{}

func (b *BrewManager) Name() string { return "brew" }
func (b *BrewManager) Install(ctx context.Context, pkg string) error {
	return exec.CommandContext(ctx, "brew", "install", pkg).Run()
}
func (b *BrewManager) IsInstalled(pkg string) bool {
	_, err := exec.LookPath(pkg)
	return err == nil
}
func (b *BrewManager) Search(pkg string) ([]PackageInfo, error) {
	out, err := exec.Command("brew", "search", pkg).Output()
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !strings.HasPrefix(name, "==>") {
			results = append(results, PackageInfo{Name: name, Source: "brew"})
		}
	}
	return results, nil
}

// AptManager manages packages via apt.
type AptManager struct{}

func (a *AptManager) Name() string { return "apt" }
func (a *AptManager) Install(ctx context.Context, pkg string) error {
	return exec.CommandContext(ctx, "sudo", "apt", "install", "-y", pkg).Run()
}
func (a *AptManager) IsInstalled(pkg string) bool {
	_, err := exec.LookPath(pkg)
	return err == nil
}
func (a *AptManager) Search(pkg string) ([]PackageInfo, error) {
	out, err := exec.Command("apt-cache", "search", pkg).Output()
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) == 2 {
			results = append(results, PackageInfo{
				Name:        strings.TrimSpace(parts[0]),
				Description: strings.TrimSpace(parts[1]),
				Source:      "apt",
			})
		}
	}
	return results, nil
}

// DnfManager manages packages via dnf (Fedora/RHEL).
type DnfManager struct{}

func (d *DnfManager) Name() string { return "dnf" }
func (d *DnfManager) Install(ctx context.Context, pkg string) error {
	return exec.CommandContext(ctx, "sudo", "dnf", "install", "-y", pkg).Run()
}
func (d *DnfManager) IsInstalled(pkg string) bool {
	_, err := exec.LookPath(pkg)
	return err == nil
}
func (d *DnfManager) Search(pkg string) ([]PackageInfo, error) {
	out, err := exec.Command("dnf", "search", pkg).Output()
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !strings.HasPrefix(name, "=") && !strings.HasPrefix(name, "Last metadata") {
			results = append(results, PackageInfo{Name: name, Source: "dnf"})
		}
	}
	return results, nil
}

// PacmanManager manages packages via pacman (Arch Linux).
type PacmanManager struct{}

func (p *PacmanManager) Name() string { return "pacman" }
func (p *PacmanManager) Install(ctx context.Context, pkg string) error {
	return exec.CommandContext(ctx, "sudo", "pacman", "-S", "--noconfirm", pkg).Run()
}
func (p *PacmanManager) IsInstalled(pkg string) bool {
	_, err := exec.LookPath(pkg)
	return err == nil
}
func (p *PacmanManager) Search(pkg string) ([]PackageInfo, error) {
	out, err := exec.Command("pacman", "-Ss", pkg).Output()
	if err != nil {
		return nil, err
	}
	var results []PackageInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name := strings.TrimSpace(line)
		if name != "" && !strings.HasPrefix(name, " ") {
			results = append(results, PackageInfo{Name: name, Source: "pacman"})
		}
	}
	return results, nil
}

// DetectPackageManager returns the best available package manager for the current system.
func DetectPackageManager() PackageManager {
	if _, err := exec.LookPath("brew"); err == nil {
		return &BrewManager{}
	}
	if _, err := exec.LookPath("apt"); err == nil {
		return &AptManager{}
	}
	if _, err := exec.LookPath("dnf"); err == nil {
		return &DnfManager{}
	}
	if _, err := exec.LookPath("pacman"); err == nil {
		return &PacmanManager{}
	}
	return &ManualManager{}
}
