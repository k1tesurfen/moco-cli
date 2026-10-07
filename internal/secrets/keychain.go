// Package secrets stores the MOCO API token in the macOS login Keychain via /usr/bin/security.
// Item: generic password, service "moco-cli", account = MOCO subdomain.
package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

const (
	service  = "moco-cli"
	security = "/usr/bin/security"
)

// ErrNotFound means there is no token for this subdomain in the Keychain.
var ErrNotFound = errors.New("no MOCO token in the Keychain — run `moco login`")

var validToken = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// GetToken returns the token stored for the subdomain.
func GetToken(subdomain string) (string, error) {
	out, err := exec.Command(security, "find-generic-password", "-s", service, "-a", subdomain, "-w").Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 44 { // errSecItemNotFound
			return "", ErrNotFound
		}
		return "", fmt.Errorf("read token from Keychain: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetToken stores (or replaces) the token for the subdomain. The token is passed on stdin
// of `security -i`, so it never shows up in the process list.
func SetToken(subdomain, token string) error {
	if !validToken.MatchString(token) {
		return errors.New("token contains unexpected characters")
	}
	if !validToken.MatchString(subdomain) {
		return errors.New("subdomain contains unexpected characters")
	}
	cmd := exec.Command(security, "-i")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -l \"MOCO API token (%s)\" -w %s\n", service, subdomain, subdomain, token))
	var stderr bytes.Buffer
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil || strings.Contains(stderr.String(), "rror") {
		return fmt.Errorf("store token in Keychain: %v %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// DeleteToken removes the token for the subdomain. A missing item is not an error.
func DeleteToken(subdomain string) error {
	err := exec.Command(security, "delete-generic-password", "-s", service, "-a", subdomain).Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 44 {
		return nil
	}
	return err
}
