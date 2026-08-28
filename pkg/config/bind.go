package config

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Bind resolution and the gateway's "may I serve without authentication?"
// decision live here so both binaries share one set of rules, and so the
// rules are testable without starting a listener.

// Bind is a resolved listen address.
type Bind struct {
	Host     string // "" means every interface
	Port     int
	Explicit bool // DELOS_BIND was set
	Loopback bool // only reachable from this machine
}

// Addr returns the address to hand to net.Listen / http.Server.
func (b Bind) Addr() string {
	if b.Host == "" {
		return fmt.Sprintf(":%d", b.Port)
	}
	return net.JoinHostPort(b.Host, fmt.Sprintf("%d", b.Port))
}

// String renders the bind for logs.
func (b Bind) String() string {
	if b.Host == "" {
		return fmt.Sprintf("0.0.0.0:%d", b.Port)
	}
	return b.Addr()
}

// IsLoopbackHost reports whether a bind host only accepts connections from
// the local machine. An empty host (every interface) is not loopback.
func IsLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]", "*":
		return false
	case "localhost":
		return true
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	// A hostname we cannot classify: assume it is reachable.
	return false
}

// ResolveBind decides what address to listen on. An explicit DELOS_BIND value
// always wins. Otherwise wideDefault chooses between every interface and
// loopback only - callers pass true once they have established that serving
// the world is safe (a token is configured, keys are enforced, or the
// operator has acknowledged the risk).
func ResolveBind(explicit string, port int, wideDefault bool) Bind {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		// Tolerate "host:port" as well as a bare host.
		host := explicit
		if h, _, err := net.SplitHostPort(explicit); err == nil {
			host = h
		}
		return Bind{Host: host, Port: port, Explicit: true, Loopback: IsLoopbackHost(host)}
	}
	if wideDefault {
		return Bind{Host: "", Port: port, Loopback: false}
	}
	return Bind{Host: "127.0.0.1", Port: port, Loopback: true}
}

// InContainer reports whether this process looks containerised. Containers
// must bind every interface to be reachable at all, so the safe-by-default
// loopback bind is not available to them - which is exactly why an
// unauthenticated gateway in a container has to be acknowledged explicitly
// rather than quietly downgraded.
func InContainer() bool {
	if v := os.Getenv("DELOS_IN_CONTAINER"); v != "" {
		return v == "1" || strings.EqualFold(v, "true")
	}
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return true
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	if data, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		s := string(data)
		for _, marker := range []string{"docker", "containerd", "kubepods", "podman", "lxc"} {
			if strings.Contains(s, marker) {
				return true
			}
		}
	}
	return false
}

// ---- gateway: fail-closed decision ----

// GatewayExposure describes everything the gateway needs to decide whether it
// may serve unauthenticated traffic.
type GatewayExposure struct {
	KeyStoreConfigured   bool // DELOS_STORAGE_BACKEND=postgres: virtual keys enforced
	AllowUnauthenticated bool // DELOS_ALLOW_UNAUTHENTICATED=true: operator acknowledgement
	ProviderCredentials  bool // at least one provider key is loaded
	BindLoopback         bool // resolved bind only accepts local connections
	InContainer          bool
}

// Gateway auth modes, as logged at startup.
const (
	// GatewayAuthEnforced means every /v1 request needs a valid virtual key.
	GatewayAuthEnforced = "enforced"
	// GatewayAuthDevLoopback means keys are not enforced, but only this
	// machine can reach the port.
	GatewayAuthDevLoopback = "dev-loopback"
	// GatewayAuthOpenAcknowledged means keys are not enforced on a reachable
	// port because the operator asked for that explicitly.
	GatewayAuthOpenAcknowledged = "open-acknowledged"
	// GatewayAuthNoProviders means keys are not enforced but there are no
	// provider credentials to front, so there is nothing to steal.
	GatewayAuthNoProviders = "open-no-providers"
)

// ErrUnauthenticatedExposure is returned when the gateway would front
// provider credentials on a reachable port with no key store. It is a startup
// error on purpose: the alternative is a credential-fronting open proxy.
type ErrUnauthenticatedExposure struct {
	Bind        string
	InContainer bool
}

func (e *ErrUnauthenticatedExposure) Error() string {
	where := fmt.Sprintf("refusing to serve on %s: provider credentials are loaded and no virtual-key store is configured.", e.Bind)
	if e.InContainer {
		where = fmt.Sprintf("refusing to serve on %s: a container must bind every interface, so this gateway is reachable by "+
			"anything that can reach the published port - with provider credentials loaded and no virtual-key store configured.", e.Bind)
	}
	return where + "\n" +
		"Anyone who can reach this port could spend your provider credits.\n\n" +
		"Fix it one of two ways:\n" +
		"  1. Enforce keys (production): set DELOS_STORAGE_BACKEND=postgres with DELOS_DB_* , then\n" +
		"     create a key with `delos keys create`.\n" +
		"  2. Acknowledge an open gateway (trusted network, laptop, demo): set\n" +
		"     DELOS_ALLOW_UNAUTHENTICATED=true.\n\n" +
		"Or bind it to localhost only with DELOS_BIND=127.0.0.1 (not reachable from a container)."
}

// DecideGatewayAuth returns the gateway's auth mode, or an error if starting
// would expose provider credentials to the network without authentication.
//
// The decision table:
//
//	key store configured        -> enforced          (always fine)
//	explicit acknowledgement    -> open-acknowledged (operator's call)
//	no provider credentials     -> open-no-providers (nothing to front)
//	loopback bind               -> dev-loopback      (local only)
//	otherwise                   -> refuse to start
func DecideGatewayAuth(e GatewayExposure) (string, error) {
	switch {
	case e.KeyStoreConfigured:
		return GatewayAuthEnforced, nil
	case e.AllowUnauthenticated:
		return GatewayAuthOpenAcknowledged, nil
	case !e.ProviderCredentials:
		return GatewayAuthNoProviders, nil
	case e.BindLoopback:
		return GatewayAuthDevLoopback, nil
	default:
		return "", &ErrUnauthenticatedExposure{InContainer: e.InContainer}
	}
}
