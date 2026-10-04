package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/buildinfo"
)

// checkPostgresAccess skips public-IP detection when the entire destination family is already allowed.
func (o *options) checkPostgresAccess(command *cobra.Command, client *api.Client, database api.PostgresDatabase, destination net.IP) error {
	for _, entry := range database.IPAllowList {
		prefix, err := netip.ParsePrefix(entry.Source)
		if err == nil && prefix.Bits() == 0 && prefix.Addr().Is4() == (destination.To4() != nil) {
			return nil
		}
	}
	source, err := postgresSourceIP(command.Context(), destination)
	if err != nil {
		return err
	}
	return o.ensurePostgresAccess(command, client, database, source)
}

// postgresSourceIP detects the direct public address in the database connection's address family.
func postgresSourceIP(ctx context.Context, destination net.IP) (netip.Addr, error) {
	endpoint := "https://api.ipify.org"
	network := "tcp4"
	if destination.To4() == nil {
		endpoint = "https://api6.ipify.org"
		network = "tcp6"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		// An HTTP proxy's public address need not be the source of a direct PostgreSQL connection.
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	request.Header.Set("User-Agent", "sliplane-cli/"+buildinfo.Version)
	response, err := client.Do(request)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("detect public IP for PostgreSQL access: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("detect public IP for PostgreSQL access: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 128))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("read public IP: %w", err)
	}
	address, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil || address.Zone() != "" || address.Is4() != (destination.To4() != nil) {
		return netip.Addr{}, fmt.Errorf("public IP service returned an invalid %s address", network)
	}
	return address, nil
}

// postgresAllowsIP supports individual addresses and IPv4/IPv6 CIDRs; an empty list blocks access.
func postgresAllowsIP(entries []api.PostgresIPRange, address netip.Addr) bool {
	address = address.Unmap()
	for _, entry := range entries {
		if source, err := netip.ParseAddr(entry.Source); err == nil && source.Unmap() == address {
			return true
		}
		if prefix, err := netip.ParsePrefix(entry.Source); err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

// ensurePostgresAccess adds only the confirmed address and preserves rules refreshed after the prompt.
func (o *options) ensurePostgresAccess(command *cobra.Command, client *api.Client, database api.PostgresDatabase, address netip.Addr) error {
	if postgresAllowsIP(database.IPAllowList, address) {
		return nil
	}
	source := netip.PrefixFrom(address, address.BitLen()).String()
	blocked := fmt.Errorf("IP %s is not allowed for database %q; add %s in the dashboard or rerun in a terminal to approve a rule", address, database.Name, source)
	if !o.canPrompt(command) {
		return blocked
	}
	question := fmt.Sprintf("IP %s is not allowed for database %q. Add a persistent allowlist rule for %s?", address, database.Name, source)
	approved, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), question)
	if err != nil {
		return err
	}
	if !approved {
		return blocked
	}
	fresh, err := client.PostgresDatabase(command.Context(), database.ID)
	if err != nil {
		return err
	}
	if postgresAllowsIP(fresh.IPAllowList, address) {
		return nil
	}
	entries := append(fresh.IPAllowList, api.PostgresIPRange{Source: source, Description: "Sliplane CLI"})
	if err := client.SetPostgresAllowList(command.Context(), database.ID, entries); err != nil {
		return fmt.Errorf("add PostgreSQL allowlist rule for %s: %w", source, err)
	}
	fmt.Fprintf(command.ErrOrStderr(), "Added allowlist rule %s. It remains until you remove it in the dashboard.\n", source)
	return nil
}
