// The optional OpenSSH integration test uses a local daemon and temporary known-hosts file.
package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

// TestSSHOpenSSH checks activation retries and an actual shell over a shared OpenSSH connection.
func TestSSHOpenSSH(t *testing.T) {
	sshd := os.Getenv("SLIPLANE_TEST_SSHD")
	if sshd == "" {
		t.Skip("set SLIPLANE_TEST_SSHD to an sshd executable for the local OpenSSH integration test")
	}
	ssh, err := exec.LookPath("ssh")
	if err != nil {
		t.Fatal(err)
	}
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Fatal(err)
	}
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the Unix control socket below the platform path limit, including on macOS.
	directory, err := os.MkdirTemp("/tmp", "slissh-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	if _, err := generateSSHKey(t.Context(), keygen, filepath.Join(directory, "host")); err != nil {
		t.Fatal(err)
	}
	identity := filepath.Join(directory, "key")
	publicKey, err := generateSSHKey(t.Context(), keygen, identity)
	if err != nil {
		t.Fatal(err)
	}
	authorized := filepath.Join(directory, "authorized_keys")
	if err := os.WriteFile(authorized, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	configuration := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nAuthorizedKeysFile %s\nPidFile %s\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nUsePAM no\nStrictModes no\nLogLevel ERROR\n", port, filepath.Join(directory, "host"), authorized, filepath.Join(directory, "pid"))
	configPath := filepath.Join(directory, "sshd_config")
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	serverLog, err := os.Create(filepath.Join(directory, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer serverLog.Close()
	server := exec.Command(sshd, "-D", "-e", "-f", configPath)
	server.Stdout = serverLog
	server.Stderr = serverLog
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			connection.Close()
			break
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(serverLog.Name())
			t.Fatalf("sshd did not start: %s", log)
		}
		time.Sleep(20 * time.Millisecond)
	}
	arguments := sshArguments(api.SSHConnection{Host: "127.0.0.1", Port: port, Username: account.Username}, identity, filepath.Join(directory, "c"))
	arguments = append(arguments, "-o", "UserKnownHostsFile="+filepath.Join(directory, "known_hosts"), "-o", "GlobalKnownHostsFile=/dev/null")
	defer closeSSHMaster(ssh, arguments, "127.0.0.1")
	activation := make(chan error, 1)
	go func() {
		time.Sleep(time.Second)
		activation <- os.WriteFile(authorized, []byte(publicKey+"\n"), 0o600)
	}()
	ready, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	masterErr := startSSHMaster(ready, ssh, arguments, "127.0.0.1", filepath.Join(directory, "connection.log"))
	if err := <-activation; err != nil {
		t.Fatal(err)
	}
	if masterErr != nil {
		log, _ := os.ReadFile(serverLog.Name())
		t.Fatalf("master: %v; server: %s", masterErr, log)
	}
	knownHosts, err := os.ReadFile(filepath.Join(directory, "known_hosts"))
	if err != nil || len(knownHosts) == 0 {
		t.Fatalf("host identity was not retained: %v", err)
	}
	command := &cobra.Command{}
	command.SetContext(ready)
	command.SetIn(strings.NewReader("printf 'SSH_NATIVE_OK\\n'; exit 7\n"))
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	err = runSSHSession(command, ssh, arguments, "127.0.0.1")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || !strings.Contains(stdout.String(), "SSH_NATIVE_OK") {
		t.Fatalf("shell: %v; stdout: %s; stderr: %s", err, stdout.String(), stderr.String())
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "SSH_TUNNEL_OK")
	}))
	defer target.Close()
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	localAddress := local.Addr().String()
	local.Close()
	tunnelContext, stopTunnel := context.WithCancel(ready)
	defer stopTunnel()
	tunnel := &cobra.Command{}
	tunnel.SetContext(tunnelContext)
	tunnel.SetIn(strings.NewReader(""))
	var tunnelOutput, tunnelErrors bytes.Buffer
	tunnel.SetOut(&tunnelOutput)
	tunnel.SetErr(&tunnelErrors)
	tunnelArguments := append(append([]string{}, arguments...), "-N", "-L", localAddress+":"+strings.TrimPrefix(target.URL, "http://"))
	tunnelDone := make(chan error, 1)
	go func() { tunnelDone <- runSSHSession(tunnel, ssh, tunnelArguments, "127.0.0.1") }()
	client := &http.Client{Timeout: time.Second}
	forwardDeadline := time.Now().Add(4 * time.Second)
	for {
		response, err := client.Get("http://" + localAddress)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil || string(body) != "SSH_TUNNEL_OK" {
				t.Fatalf("tunnel response: %q %v", body, readErr)
			}
			break
		}
		select {
		case err := <-tunnelDone:
			t.Fatalf("tunnel exited before forwarding: %v; %s", err, tunnelErrors.String())
		default:
		}
		if time.Now().After(forwardDeadline) {
			stopTunnel()
			<-tunnelDone
			t.Fatalf("tunnel did not forward traffic: %v; %s", err, tunnelErrors.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	client.CloseIdleConnections()
	// A tunnel must survive longer than the master's idle timeout even without traffic.
	time.Sleep(31 * time.Second)
	response, err := client.Get("http://" + localAddress)
	if err != nil {
		stopTunnel()
		<-tunnelDone
		t.Fatalf("tunnel stopped while idle: %v; %s", err, tunnelErrors.String())
	}
	response.Body.Close()
	stopTunnel()
	if err := <-tunnelDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("tunnel cancellation: %v; %s", err, tunnelErrors.String())
	}
	closeSSHMaster(ssh, arguments, "127.0.0.1")
	wrongHostKey := fmt.Sprintf("[127.0.0.1]:%d %s\n", port, publicKey)
	if err := os.WriteFile(filepath.Join(directory, "known_hosts"), []byte(wrongHostKey), 0o600); err != nil {
		t.Fatal(err)
	}
	err = startSSHMaster(ready, ssh, arguments, "127.0.0.1", filepath.Join(directory, "connection.log"))
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("changed host key was not rejected immediately: %v", err)
	}
}
