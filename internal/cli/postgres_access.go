package cli

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

// checkPostgresAccess skips public-IP detection when the entire destination family is already allowed.
func (o *options) checkPostgresAccess(command *cobra.Command, client *api.Client, database api.PostgresDatabase, destination net.IP) error {
	for _, entry := range database.IPAllowList {
		prefix, err := netip.ParsePrefix(entry.Source)
		if err == nil && prefix.Bits() == 0 && prefix.Addr().Is4() == (destination.To4() != nil) {
			return nil
		}
	}
	source, err := postgresSourceIP(command.Context(), client, destination)
	if err != nil {
		return err
	}
	return o.ensurePostgresAccess(command, client, database, source)
}

// postgresSourceIP asks the API for the caller's public address in the database connection's address family.
func postgresSourceIP(ctx context.Context, client *api.Client, destination net.IP) (netip.Addr, error) {
	network := "tcp4"
	if destination.To4() == nil {
		network = "tcp6"
	}
	address, err := client.ClientIP(ctx, network)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("detect public IP for PostgreSQL access: %w", err)
	}
	if address.Is4() != (destination.To4() != nil) {
		return netip.Addr{}, fmt.Errorf("detect public IP for PostgreSQL access: API returned an address outside %s", network)
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
	blocked := fmt.Errorf("IP %s is not allowed for database %q; run sliplane postgres allowlist add %s %s or rerun in a terminal to approve a rule", address, database.Name, database.ID, source)
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
	fmt.Fprintf(command.ErrOrStderr(), "Added allowlist rule %s. Remove it with sliplane postgres allowlist remove %s %s.\n", source, database.ID, source)
	return nil
}
