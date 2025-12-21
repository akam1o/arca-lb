package frr

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
)

// VTYShell executes vtysh commands for FRR configuration
type VTYShell interface {
	// Execute runs a vtysh command and returns output
	Execute(ctx context.Context, command string) (string, error)

	// ExecuteCommands runs multiple vtysh commands using separate -c flags
	// This is more compatible across FRR versions than newline-separated commands
	ExecuteCommands(ctx context.Context, commands []string) (string, error)
}

// VTYSh implements VTYShell interface for executing vtysh commands
type VTYSh struct {
	vtyshPath string
	logger    *logrus.Logger
	timeout   time.Duration
}

// NewVTYSh creates a new VTYSh instance
func NewVTYSh(vtyshPath string, logger *logrus.Logger) *VTYSh {
	return &VTYSh{
		vtyshPath: vtyshPath,
		logger:    logger,
		timeout:   5 * time.Second, // Default 5 second timeout
	}
}

// Execute runs a vtysh command with timeout
func (v *VTYSh) Execute(ctx context.Context, command string) (string, error) {
	// Apply timeout if not already set in context
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	v.logger.WithFields(logrus.Fields{
		"command": command,
		"vtysh":   v.vtyshPath,
	}).Debug("Executing vtysh command")

	// Execute vtysh command with -c flag
	cmd := exec.CommandContext(ctx, v.vtyshPath, "-c", command)
	output, err := cmd.CombinedOutput()

	if err != nil {
		v.logger.WithError(err).WithFields(logrus.Fields{
			"command": command,
			"output":  string(output),
		}).Error("vtysh command failed")

		return "", fmt.Errorf("vtysh command failed: %w (output: %s)", err, string(output))
	}

	v.logger.WithFields(logrus.Fields{
		"command": command,
		"output":  string(output),
	}).Debug("vtysh command succeeded")

	return string(output), nil
}

// ExecuteCommands runs multiple vtysh commands using separate -c flags
// This is more compatible across FRR versions than newline-separated commands
func (v *VTYSh) ExecuteCommands(ctx context.Context, commands []string) (string, error) {
	// Apply timeout if not already set in context
	ctx, cancel := context.WithTimeout(ctx, v.timeout)
	defer cancel()

	v.logger.WithFields(logrus.Fields{
		"commands": commands,
		"vtysh":    v.vtyshPath,
	}).Debug("Executing vtysh commands")

	// Build argument list with multiple -c flags
	args := []string{}
	for _, cmd := range commands {
		args = append(args, "-c", cmd)
	}

	// Execute vtysh with multiple -c arguments
	cmd := exec.CommandContext(ctx, v.vtyshPath, args...)
	output, err := cmd.CombinedOutput()

	if err != nil {
		v.logger.WithError(err).WithFields(logrus.Fields{
			"commands": commands,
			"output":   string(output),
		}).Error("vtysh commands failed")

		return "", fmt.Errorf("vtysh commands failed: %w (output: %s)", err, string(output))
	}

	v.logger.WithFields(logrus.Fields{
		"commands": commands,
		"output":   string(output),
	}).Debug("vtysh commands succeeded")

	return string(output), nil
}

// SetTimeout updates the vtysh command timeout
func (v *VTYSh) SetTimeout(timeout time.Duration) {
	v.timeout = timeout
}
