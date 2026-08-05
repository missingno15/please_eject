// Package volume resolves external volumes by name and handles ejection via
// macOS's diskutil.
package volume

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Info describes a mounted volume.
type Info struct {
	Name       string // Volume Name, e.g. "My Passport for Mac"
	MountPoint string // e.g. "/Volumes/My Passport for Mac"
	DeviceNode string // e.g. "/dev/disk5s1"
}

// Resolve looks up a volume by its name using `diskutil info`. If diskutil
// cannot find it, it falls back to the conventional /Volumes/<name> path.
func Resolve(name string) (Info, error) {
	info := Info{Name: name}

	out, err := exec.Command("diskutil", "info", name).Output()
	if err == nil {
		parseDiskutil(string(out), &info)
	}

	if info.MountPoint == "" {
		// Fallback to the conventional mount location.
		guess := filepath.Join("/Volumes", name)
		if st, statErr := os.Stat(guess); statErr == nil && st.IsDir() {
			info.MountPoint = guess
		}
	}

	if info.MountPoint == "" {
		return info, fmt.Errorf("could not find a mounted volume named %q", name)
	}
	if info.Name == "" {
		info.Name = name
	}
	return info, nil
}

func parseDiskutil(out string, info *Info) {
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "Volume Name":
			if val != "" {
				info.Name = val
			}
		case "Mount Point":
			info.MountPoint = val
		case "Device Node":
			info.DeviceNode = val
		}
	}
}

// IsMounted reports whether the volume still appears mounted.
func (i Info) IsMounted() bool {
	if i.MountPoint == "" {
		return false
	}
	st, err := os.Stat(i.MountPoint)
	return err == nil && st.IsDir()
}

// SpotlightOffCmd returns a command that turns off Spotlight indexing for this
// volume. Changing indexing state requires root, so it is wrapped in sudo; the
// TUI runs it via tea.ExecProcess so the user can enter their password on the
// real terminal. Turning indexing off tells Spotlight's mds/mdworker daemons to
// release the volume, which is a common last-resort blocker for ejection.
func (i Info) SpotlightOffCmd() *exec.Cmd {
	return exec.Command("sudo", "mdutil", "-i", "off", i.MountPoint)
}

// SpotlightStatus reports the volume's Spotlight indexing state as a short,
// human-readable string (best effort; empty if it can't be determined).
func (i Info) SpotlightStatus() string {
	out, _ := exec.Command("mdutil", "-s", i.MountPoint).CombinedOutput()
	text := strings.TrimSpace(string(out))
	switch {
	case text == "":
		return ""
	case strings.Contains(text, "Indexing enabled"):
		return "on"
	case strings.Contains(text, "Indexing disabled"):
		return "off"
	case strings.Contains(text, "invalid path"), strings.Contains(text, "unknown indexing state"):
		return "not indexed"
	default:
		return ""
	}
}

// Eject asks diskutil to eject the volume. It returns diskutil's message so the
// caller can surface exactly why an eject failed (e.g. still in use).
func (i Info) Eject() error {
	target := i.Name
	if target == "" {
		target = i.MountPoint
	}
	out, err := exec.Command("diskutil", "eject", target).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return errors.New(msg)
	}
	return nil
}
