package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) tunnelCommand() *cobra.Command {
	var localPort, remotePort int
	var ttl time.Duration
	command := &cobra.Command{
		Use: "tunnel <name-or-id>", Short: "Forward a local port to a service", Args: validateArguments,
		Long:    "Connect to a service through a local TCP port. Uses the service's internal port locally when available, otherwise a free port.\nIf the service port is unknown, tries HTTP on port 80.\nKeep the command running while you use the connection; press Ctrl-C to stop.\nRequires a read-write token, OpenSSH, and an SSH-enabled service.",
		Example: "  sliplane tunnel api\n  sliplane tunnel database --local-port 5432\n  sliplane tunnel api --remote-port 8080",
		RunE: func(command *cobra.Command, args []string) error {
			if o.output != "text" {
				return errors.New("tunnels require --output text")
			}
			if localPort < 0 || localPort > 65535 {
				return errors.New("--local-port must be between 1 and 65535, or 0 to choose a free port")
			}
			if command.Flags().Changed("remote-port") && (remotePort < 1 || remotePort > 65535) {
				return errors.New("--remote-port must be between 1 and 65535")
			}
			if ttl <= 5*time.Minute || ttl > 30*24*time.Hour {
				return errors.New("--ttl must be longer than 5m and at most 720h (30 days); use 6m or longer to allow time for the API request")
			}
			ssh, err := exec.LookPath("ssh")
			if err != nil {
				return errors.New("ssh was not found; install OpenSSH and make sure ssh is on PATH")
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
			var network api.ServiceNetworkDetails
			if len(service.Network) > 0 {
				if err := json.Unmarshal(service.Network, &network); err != nil {
					return fmt.Errorf("read service network: %w", err)
				}
			}
			if network.Protocol == "udp" {
				return errors.New("SSH tunnels support TCP connections only")
			}
			if !command.Flags().Changed("remote-port") {
				remotePort = network.InternalPort
			}
			probePort := 0
			if remotePort == 0 {
				remotePort = 80
				probePort = remotePort
			}
			if remotePort < 1 || remotePort > 65535 {
				return errors.New("service internal port is unavailable; specify --remote-port")
			}
			automaticLocalPort := !command.Flags().Changed("local-port")
			if automaticLocalPort {
				localPort = remotePort
			}
			listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort)))
			if err != nil && automaticLocalPort {
				listener, err = net.Listen("tcp4", "127.0.0.1:0")
			}
			if err != nil {
				return fmt.Errorf("open local port: %w", err)
			}
			address := listener.Addr().String()
			if err := listener.Close(); err != nil {
				return err
			}
			fmt.Fprintf(command.ErrOrStderr(), "Forwarding %s to %s:%d\n", address, service.Name, remotePort)
			return openSSH(command, client, service, ssh, keygen, ttl, []string{"-N", "-L", address + ":localhost:" + strconv.Itoa(remotePort)}, probePort)
		},
	}
	command.Flags().IntVar(&localPort, "local-port", 0, "Local port (defaults to service port if available; 0 chooses a free port)")
	command.Flags().IntVar(&remotePort, "remote-port", 0, "Service port (uses the service's internal port, or tries HTTP on port 80)")
	command.Flags().DurationVar(&ttl, "ttl", time.Hour, "Fallback lifetime of the temporary SSH key (more than 5m, up to 720h)")
	return command
}

func probeHTTPPort(ctx context.Context, ssh string, arguments []string, host string, port int) error {
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append(append([]string{}, arguments...), "-S", "none", "-W", net.JoinHostPort("localhost", strconv.Itoa(port)), "--", host)
	process := exec.CommandContext(probe, ssh, args...)
	process.Stdin = strings.NewReader("HEAD / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
	stdout, err := process.StdoutPipe()
	if err != nil {
		return err
	}
	if err := process.Start(); err != nil {
		return err
	}
	defer func() {
		cancel()
		_ = process.Wait()
	}()
	response, err := http.ReadResponse(bufio.NewReader(stdout), &http.Request{Method: http.MethodHead})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("could not reach HTTP on port %d; specify --remote-port", port)
	}
	return response.Body.Close()
}
