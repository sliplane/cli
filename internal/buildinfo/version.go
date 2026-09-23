// Build metadata is shared by version reporting and outgoing HTTP clients.
package buildinfo

// Version defaults to dev for plain Go builds; make build embeds it through -ldflags -X.
var Version = "dev"
