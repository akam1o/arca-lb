package models

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
)

// ValidateHealthCheckConfig validates the runtime health check config map.
func ValidateHealthCheckConfig(hcType HCType, config HCConfig) error {
	switch hcType {
	case HCTypeHTTP, HCTypeHTTPS, HCTypeTCP, HCTypeTLSHello:
		if config == nil {
			return fmt.Errorf("config is required for %s health checks", hcType)
		}
		if _, err := healthCheckConfigInt(config, "port", 1, 65535); err != nil {
			return err
		}
	case HCTypePing:
		return nil
	default:
		return fmt.Errorf("unsupported health check type: %s", hcType)
	}

	switch hcType {
	case HCTypeHTTP, HCTypeHTTPS:
		return validateHTTPHealthCheckConfig(config)
	case HCTypeTCP:
		return validateTCPHealthCheckConfig(config)
	case HCTypeTLSHello, HCTypePing:
		return nil
	default:
		return fmt.Errorf("unsupported health check type: %s", hcType)
	}
}

// ValidateHealthCheckTiming validates health check timing and threshold fields.
func ValidateHealthCheckTiming(hc *HealthCheck) error {
	if hc == nil {
		return fmt.Errorf("health check is required")
	}
	if hc.IntervalSec < 1 || hc.IntervalSec > MaxHealthCheckSeconds {
		return fmt.Errorf("health check interval_sec must be between 1 and %d, got %d", MaxHealthCheckSeconds, hc.IntervalSec)
	}
	if hc.TimeoutSec < 1 || hc.TimeoutSec > MaxHealthCheckSeconds {
		return fmt.Errorf("health check timeout_sec must be between 1 and %d, got %d", MaxHealthCheckSeconds, hc.TimeoutSec)
	}
	if hc.TimeoutSec >= hc.IntervalSec {
		return fmt.Errorf("health check timeout_sec must be less than interval_sec")
	}
	if hc.RiseCount < 1 || hc.RiseCount > MaxHealthCheckCount {
		return fmt.Errorf("health check rise_count must be between 1 and %d, got %d", MaxHealthCheckCount, hc.RiseCount)
	}
	if hc.FallCount < 1 || hc.FallCount > MaxHealthCheckCount {
		return fmt.Errorf("health check fall_count must be between 1 and %d, got %d", MaxHealthCheckCount, hc.FallCount)
	}
	return nil
}

func validateHTTPHealthCheckConfig(config HCConfig) error {
	if err := optionalStringHealthCheckConfig(config, "path"); err != nil {
		return err
	}
	if err := validateHTTPPathConfig(config["path"]); err != nil {
		return err
	}
	if err := optionalStringHealthCheckConfig(config, "method"); err != nil {
		return err
	}
	if err := validateHTTPMethodConfig(config["method"]); err != nil {
		return err
	}
	if err := optionalStringHealthCheckConfig(config, "host_header"); err != nil {
		return err
	}
	if raw, ok := config["tls_skip_verify"]; ok && raw != nil {
		if _, ok := raw.(bool); !ok {
			return fmt.Errorf("tls_skip_verify must be a boolean")
		}
	}
	if err := validateExpectedCodes(config["expected_codes"]); err != nil {
		return err
	}
	return validateHeaders(config["headers"])
}

func validateHTTPPathConfig(raw any) error {
	if raw == nil {
		return nil
	}
	path, ok := raw.(string)
	if !ok || path == "" {
		return nil
	}
	parsedPath, err := url.Parse(path)
	if err != nil {
		return fmt.Errorf("path must be a valid relative HTTP path: %w", err)
	}
	if parsedPath.IsAbs() || parsedPath.Host != "" {
		return fmt.Errorf("path must be a relative HTTP path")
	}
	return nil
}

func validateHTTPMethodConfig(raw any) error {
	if raw == nil {
		return nil
	}
	method, ok := raw.(string)
	if !ok || method == "" {
		return nil
	}
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPost:
		return nil
	default:
		return fmt.Errorf("method must be one of GET, HEAD, POST")
	}
}

func validateTCPHealthCheckConfig(config HCConfig) error {
	if err := optionalStringHealthCheckConfig(config, "send"); err != nil {
		return err
	}
	return optionalStringHealthCheckConfig(config, "expect")
}

func optionalStringHealthCheckConfig(config HCConfig, key string) error {
	raw, ok := config[key]
	if !ok || raw == nil {
		return nil
	}
	if _, ok := raw.(string); !ok {
		return fmt.Errorf("%s must be a string", key)
	}
	return nil
}

func healthCheckConfigInt(config HCConfig, key string, minValue, maxValue int) (int, error) {
	raw, ok := config[key]
	if !ok || raw == nil {
		return 0, fmt.Errorf("%s is required", key)
	}

	value, ok := healthCheckInt(raw)
	if !ok || value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minValue, maxValue)
	}
	return value, nil
}

func validateExpectedCodes(raw any) error {
	if raw == nil {
		return nil
	}

	switch codes := raw.(type) {
	case []interface{}:
		for _, code := range codes {
			if err := validateExpectedCode(code); err != nil {
				return err
			}
		}
	case []int:
		for _, code := range codes {
			if err := validateExpectedCode(code); err != nil {
				return err
			}
		}
	case []float64:
		for _, code := range codes {
			if err := validateExpectedCode(code); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("expected_codes must be an array of integers")
	}
	return nil
}

func validateExpectedCode(raw any) error {
	code, ok := healthCheckInt(raw)
	if !ok || code < 100 || code > 599 {
		return fmt.Errorf("expected_codes must be integers between 100 and 599")
	}
	return nil
}

func validateHeaders(raw any) error {
	if raw == nil {
		return nil
	}

	switch headers := raw.(type) {
	case map[string]interface{}:
		for _, value := range headers {
			if value == nil {
				continue
			}
			if _, ok := value.(string); !ok {
				return fmt.Errorf("headers values must be strings")
			}
		}
	case map[string]string:
		return nil
	default:
		return fmt.Errorf("headers must be an object")
	}
	return nil
}

func healthCheckInt(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
			return 0, false
		}
		return int(value), true
	default:
		return 0, false
	}
}
