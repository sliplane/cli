// SSH sessions use a private temporary identity and the installed OpenSSH client.
package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"sliplane-cli/internal/api"
)

// sshCommand opens a shell without requiring a saved SSH key or a separate connect command.
func (o *options) sshCommand() *cobra.Command {
	var ttl time.Duration
	var localForwards []string
	var noShell bool
	command := &cobra.Command{
		Use: "ssh <name-or-id>", Short: "Open a service shell or SSH tunnel with a temporary key", Args: validateArguments,
		Long:    "Open a service shell or tunnel using your Sliplane API token and the installed OpenSSH client.\nUse -L for local port forwarding and -N to run without a shell.\nThe temporary key is removed after the session. Scheduled deletion is a fallback\nif the CLI is interrupted. Requires a read-write token and SSH-enabled service.",
		Example: "  sliplane ssh api\n  sliplane ssh api --project production\n  sliplane ssh api --ttl 2h\n  sliplane ssh database -N -L 5432:localhost:5432",
		RunE: func(command *cobra.Command, args []string) error {
			if o.output != "text" {
				return errors.New("SSH sessions require --output text")
			}
			if ttl <= 5*time.Minute || ttl > 30*24*time.Hour {
				return errors.New("--ttl must be longer than 5m and at most 720h (30 days); use 6m or longer to allow time for the API request")
			}
			ssh, err := exec.LookPath("ssh")
			if err != nil {
				return errors.New("ssh was not found; install OpenSSH and make sure ssh is on PATH")
			}
			if noShell && len(localForwards) == 0 {
				return errors.New("-N requires a local forward; for example: sliplane ssh database -N -L 5432:localhost:5432")
			}
			sessionArguments := []string{}
			for _, forward := range localForwards {
				if strings.TrimSpace(forward) == "" {
					return errors.New("-L requires [bind_address:]port:host:hostport")
				}
				sessionArguments = append(sessionArguments, "-L", forward)
			}
			if noShell {
				sessionArguments = append(sessionArguments, "-N")
			}
			if len(sessionArguments) > 0 {
				validation := append([]string{"-G", "-F", "none"}, sessionArguments...)
				validation = append(validation, "--", "localhost")
				if output, err := exec.CommandContext(command.Context(), ssh, validation...).CombinedOutput(); err != nil {
					return fmt.Errorf("invalid SSH forwarding options: %w: %s", err, strings.TrimSpace(string(output)))
				}
			}
			keygen, err := exec.LookPath("ssh-keygen")
			if err != nil {
				return errors.New("ssh-keygen was not found; install OpenSSH and make sure ssh-keygen is on PATH")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			return openSSH(command, client, service, ssh, keygen, ttl, sessionArguments)
		},
	}
	command.Flags().DurationVar(&ttl, "ttl", time.Hour, "Fallback lifetime of the temporary SSH key (more than 5m, up to 720h)")
	command.Flags().StringArrayVarP(&localForwards, "local-forward", "L", nil, "Forward [bind_address:]port:host:hostport through the service (repeatable)")
	command.Flags().BoolVarP(&noShell, "no-shell", "N", false, "Keep tunnels open without starting a remote shell")
	return command
}

// validateSSHConnection rejects missing details and values that are not SSH destinations.
func validateSSHConnection(connection api.SSHConnection) error {
	if connection.Port < 1 || connection.Port > 65535 || connection.Username == "" || strings.ContainsAny(connection.Username, "\x00 \t\r\n") {
		return errors.New("API returned invalid SSH connection details")
	}
	if net.ParseIP(connection.Host) != nil {
		return nil
	}
	if connection.Host == "" || strings.HasPrefix(connection.Host, "-") {
		return errors.New("API returned an invalid SSH host")
	}
	for _, character := range connection.Host {
		if character != '.' && character != '-' && !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') {
			return errors.New("API returned an invalid SSH host")
		}
	}
	return nil
}

// openSSH owns the local identity, remote grant, and connection for one shell or tunnel.
func openSSH(command *cobra.Command, client *api.Client, service api.Service, ssh, keygen string, ttl time.Duration, sessionArguments []string) (result error) {
	directory, err := os.MkdirTemp("", "slissh-")
	if err != nil {
		return fmt.Errorf("create private SSH directory: %w", err)
	}
	defer func() { result = errors.Join(result, os.RemoveAll(directory)) }()
	identity := filepath.Join(directory, "key")
	publicKey, err := generateSSHKey(command.Context(), keygen, identity)
	if err != nil {
		return err
	}
	expiresAt := time.Now().UTC().Add(ttl)
	key, err := client.CreateSSHKey(command.Context(), "Sliplane CLI: "+service.Name, publicKey, expiresAt)
	if err != nil {
		return fmt.Errorf("create temporary SSH key: %w", err)
	}
	if key.ID == "" {
		return errors.New("API created an SSH key without returning its ID; cannot attach it to the service")
	}
	defer func() {
		if err := cleanupSSHKey(command.Context(), client, service, key.ID); err != nil {
			result = errors.Join(result, fmt.Errorf("clean up temporary SSH key %s (automatic deletion scheduled for %s): %w", key.ID, expiresAt.Format(time.RFC3339), err))
		}
	}()
	connection, err := client.AttachSSHKey(command.Context(), service.ProjectID, service.ID, key.ID)
	if err != nil {
		return fmt.Errorf("attach temporary SSH key: %w", err)
	}
	if err := validateSSHConnection(connection); err != nil {
		return err
	}

	control := filepath.Join(directory, "c")
	arguments := sshArguments(connection, identity, control)
	defer closeSSHMaster(ssh, arguments, connection.Host)
	fmt.Fprintln(command.ErrOrStderr(), "Waiting for SSH access...")
	ready, cancel := context.WithTimeout(command.Context(), time.Minute)
	defer cancel()
	if err := startSSHMaster(ready, ssh, arguments, connection.Host, filepath.Join(directory, "connection.log")); err != nil {
		if command.Context().Err() != nil {
			return command.Context().Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("SSH access did not become ready within 1 minute; retry shortly: %w", err)
		}
		return err
	}
	if slices.Contains(sessionArguments, "-N") {
		fmt.Fprintf(command.ErrOrStderr(), "Starting SSH tunnel to %s (Ctrl-C to stop)...\n", service.Name)
	} else {
		fmt.Fprintf(command.ErrOrStderr(), "Connecting to %s...\n", service.Name)
	}
	return runSSHSession(command, ssh, append(arguments, sessionArguments...), connection.Host)
}

// generateSSHKey lets OpenSSH create its native private-key format inside a mode-0700 directory.
func generateSSHKey(ctx context.Context, keygen, path string) (string, error) {
	process := exec.CommandContext(ctx, keygen, "-q", "-t", "ed25519", "-N", "", "-C", "sliplane-cli", "-f", path)
	if output, err := process.CombinedOutput(); err != nil {
		return "", errors.Join(ctx.Err(), fmt.Errorf("generate temporary SSH key: %w: %s", err, strings.TrimSpace(string(output))))
	}
	publicKey, err := os.ReadFile(path + ".pub")
	if err != nil {
		return "", fmt.Errorf("read SSH public key: %w", err)
	}
	return strings.TrimSpace(string(publicKey)), nil
}

// sshArguments isolate this identity and connection while retaining persistent host-key checks.
func sshArguments(connection api.SSHConnection, identity, control string) []string {
	return []string{
		"-F", "none", "-i", identity, "-S", control,
		"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=10",
		"-o", "ConnectionAttempts=1", "-o", "ControlPersist=30",
		"-o", "ExitOnForwardFailure=yes",
		"-p", strconv.Itoa(connection.Port), "-l", connection.Username,
	}
}

// startSSHMaster retries authentication before any shell opens; at most 12 attempts avoid server lockouts.
func startSSHMaster(ctx context.Context, ssh string, arguments []string, host, logPath string) error {
	var lastError error
	for attempt := 0; attempt < 12; attempt++ {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, lastError)
		}
		log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		process := exec.CommandContext(ctx, ssh, append(append([]string{}, arguments...), "-f", "-N", "-M", "--", host)...)
		// A file avoids inherited pipe handles keeping Wait blocked after ssh backgrounds itself.
		process.Stdout = log
		process.Stderr = log
		err = process.Run()
		_ = log.Close()
		if err == nil {
			return nil
		}
		diagnostics, readErr := os.ReadFile(logPath)
		if readErr != nil {
			return errors.Join(err, readErr)
		}
		message := strings.TrimSpace(string(diagnostics))
		lastError = fmt.Errorf("open SSH connection: %w: %s", err, message)
		if !strings.Contains(message, "Permission denied (publickey)") {
			return errors.Join(ctx.Err(), lastError)
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), lastError)
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("SSH key did not become active within 1 minute; retry shortly: %w", lastError)
}

// runSSHSession reuses the authenticated connection and restores terminal state even after cancellation.
func runSSHSession(command *cobra.Command, ssh string, arguments []string, host string) error {
	if input, ok := command.InOrStdin().(*os.File); ok && term.IsTerminal(int(input.Fd())) {
		state, err := term.GetState(int(input.Fd()))
		if err != nil {
			return err
		}
		defer term.Restore(int(input.Fd()), state)
	}
	processArguments := append([]string{}, arguments...)
	if slices.Contains(arguments, "-N") {
		// A multiplexed -N client registers its forwards and exits; a tunnel needs a foreground connection.
		processArguments = append(processArguments, "-S", "none")
	}
	process := exec.CommandContext(command.Context(), ssh, append(processArguments, "--", host)...)
	process.Stdin = command.InOrStdin()
	process.Stdout = command.OutOrStdout()
	process.Stderr = command.ErrOrStderr()
	process.Cancel = func() error { return process.Process.Signal(os.Interrupt) }
	process.WaitDelay = 2 * time.Second
	if err := process.Run(); err != nil {
		return errors.Join(command.Context().Err(), fmt.Errorf("SSH session ended: %w", err))
	}
	return nil
}

// closeSSHMaster also runs when authentication was interrupted after the control socket was created.
func closeSSHMaster(ssh string, arguments []string, host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, ssh, append(append([]string{}, arguments...), "-O", "exit", "--", host)...).Run()
}

// cleanupSSHKey can run after cancellation and tolerates a key already removed by scheduled expiry.
func cleanupSSHKey(ctx context.Context, client *api.Client, service api.Service, keyID string) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	detachErr := client.DetachSSHKey(cleanup, service.ProjectID, service.ID, keyID)
	var apiError *api.Error
	if errors.As(detachErr, &apiError) && apiError.Status == http.StatusNotFound {
		detachErr = nil
	}
	deleteErr := client.DeleteSSHKey(cleanup, keyID)
	if errors.As(deleteErr, &apiError) && apiError.Status == http.StatusNotFound {
		deleteErr = nil
	}
	return errors.Join(detachErr, deleteErr)
}
