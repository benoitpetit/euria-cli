package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const updateRepo = "benoitpetit/euria-cli"

func runUpdate(ctx context.Context, stdout, stderr io.Writer, opts Options) error {
	version := "latest"
	if opts.Subcommand != "" && opts.Subcommand != "update" {
		version = opts.Subcommand
	}
	// Allow `euria update v0.0.5`
	if len(opts.Positional) > 0 {
		version = opts.Positional[0]
	}

	osPart, archPart, asset, err := updateAssetName()
	if err != nil {
		return err
	}
	_ = osPart
	_ = archPart

	base := "https://github.com/" + updateRepo + "/releases"
	var url, sumsURL string
	if version == "latest" {
		url = base + "/latest/download/" + asset
		sumsURL = base + "/latest/download/SHA256SUMS"
	} else {
		url = base + "/download/" + version + "/" + asset
		sumsURL = base + "/download/" + version + "/SHA256SUMS"
	}

	fmt.Fprintf(stderr, "Downloading %s (%s)...\n", asset, version)

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate current binary: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve binary path: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "euria-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	assetPath := filepath.Join(tmpDir, asset)
	if err := downloadFile(ctx, url, assetPath); err != nil {
		return err
	}
	sumsPath := filepath.Join(tmpDir, "SHA256SUMS")
	if err := downloadFile(ctx, sumsURL, sumsPath); err != nil {
		return err
	}

	// verify checksum
	data, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	expected := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == asset {
			expected = fields[0]
			break
		}
	}
	if expected != "" {
		f, err := os.Open(assetPath)
		if err != nil {
			return err
		}
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		actual := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(actual, expected) {
			return fmt.Errorf("checksum verification failed")
		}
	}

	// replace binary
	backup := exe + ".old"
	_ = os.Remove(backup)
	if err := os.Rename(exe, backup); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}
	in, err := os.Open(assetPath)
	if err != nil {
		_ = os.Rename(backup, exe)
		return err
	}
	out, err := os.OpenFile(exe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		in.Close()
		_ = os.Rename(backup, exe)
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		in.Close()
		_ = os.Rename(backup, exe)
		return err
	}
	out.Close()
	in.Close()
	_ = os.Remove(backup)

	fmt.Fprintln(stdout, "euria updated successfully")
	// print version of new binary? attempt
	cmd := exec.Command(exe, "--version")
	if out, err := cmd.Output(); err == nil {
		fmt.Fprint(stdout, string(out))
	}
	return nil
}

func updateAssetName() (string, string, string, error) {
	osPart := runtime.GOOS
	archPart := runtime.GOARCH
	var osName, archName string
	switch osPart {
	case "linux":
		osName = "linux"
	case "darwin":
		osName = "darwin"
	case "windows":
		osName = "windows"
	default:
		return "", "", "", fmt.Errorf("unsupported OS: %s", osPart)
	}
	switch archPart {
	case "amd64":
		archName = "amd64"
	case "arm64":
		archName = "arm64"
	case "arm":
		archName = "armv7"
	default:
		return "", "", "", fmt.Errorf("unsupported architecture: %s", archPart)
	}
	asset := "euria_" + osName + "_" + archName
	if osPart == "windows" {
		asset += ".exe"
	}
	if osPart == "linux" && archPart == "arm" {
		asset = "euria_linux_armv7"
	}
	return osName, archName, asset, nil
}

func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

