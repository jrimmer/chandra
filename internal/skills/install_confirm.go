package skills

import "fmt"

// InstallConfirmation holds package metadata for the user to review before install.
type InstallConfirmation struct {
	PackageName string
	Version     string
	Description string
	Source      string // e.g. "brew", "apt", "manual"
	Command     string // e.g. "brew install gh"
	Effects     string // e.g. "Download ~15MB, add gh to /usr/bin"
}

// BuildInstallConfirmation creates confirmation details for a package installation.
func BuildInstallConfirmation(pm PackageManager, pkg string) (*InstallConfirmation, error) {
	confirm := &InstallConfirmation{
		PackageName: pkg,
		Source:      pm.Name(),
	}

	// Search for package info.
	infos, err := pm.Search(pkg)
	if err == nil && len(infos) > 0 {
		// Use the first matching result.
		for _, info := range infos {
			if info.Name == pkg {
				confirm.Version = info.Version
				confirm.Description = info.Description
				break
			}
		}
		// Fallback to first result if exact match not found.
		if confirm.Description == "" && len(infos) > 0 {
			confirm.Description = infos[0].Description
		}
	}

	// Build the install command string.
	switch pm.Name() {
	case "brew":
		confirm.Command = fmt.Sprintf("brew install %s", pkg)
	case "apt":
		confirm.Command = fmt.Sprintf("sudo apt install -y %s", pkg)
	case "dnf":
		confirm.Command = fmt.Sprintf("sudo dnf install -y %s", pkg)
	case "pacman":
		confirm.Command = fmt.Sprintf("sudo pacman -S --noconfirm %s", pkg)
	default:
		confirm.Command = fmt.Sprintf("install %s manually", pkg)
	}

	return confirm, nil
}

// Format returns a human-readable summary of the install confirmation.
func (c *InstallConfirmation) Format() string {
	out := fmt.Sprintf("Package: %s\n", c.PackageName)
	if c.Version != "" {
		out += fmt.Sprintf("Version: %s\n", c.Version)
	}
	if c.Description != "" {
		out += fmt.Sprintf("Description: %s\n", c.Description)
	}
	out += fmt.Sprintf("Source: %s\n", c.Source)
	out += fmt.Sprintf("Command: %s\n", c.Command)
	if c.Effects != "" {
		out += fmt.Sprintf("Effects: %s\n", c.Effects)
	}
	return out
}
