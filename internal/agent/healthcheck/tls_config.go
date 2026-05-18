package healthcheck

import "crypto/tls"

func newHealthCheckTLSConfig(skipVerify bool) *tls.Config {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if skipVerify {
		cfg.InsecureSkipVerify = true //nolint:gosec // Health checks may intentionally skip backend certificate verification.
	}
	return cfg
}
