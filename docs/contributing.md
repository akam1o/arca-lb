# arca-lb Contribution Guide

This document explains how to contribute to the arca-lb project.

## Ways to Contribute

We welcome contributions such as:

- Bug reports
- Feature requests
- Documentation improvements
- Code improvements
- Additional tests

## Development Process

### 1. Create an issue

Open bug reports and feature requests in GitHub Issues.

**Bug report template**:

```markdown
## Bug Description
Describe the bug concisely.

## Steps to Reproduce
1. ...
2. ...
3. ...

## Expected Behavior
What should happen?

## Actual Behavior
What actually happened?

## Environment
- OS:
- Go version:
- Kubernetes version:
- arca-lb version:
```

### 2. Create a branch

```bash
# Get the latest main
git checkout main
git pull origin main

# Create a feature branch
git checkout -b feature/my-feature
# or
git checkout -b fix/my-bugfix
```

### 3. Make changes

- Follow the code style (see [Development Environment](./development.md)).
- Add or update tests.
- Update documentation.
- If modifying CRD types, run `make manifests generate`.

### 4. Commit

```bash
# Stage changes
git add .

# Commit with a clear message
git commit -m "feat: add new feature"
# or
git commit -m "fix: fix bug in VIP creation"
```

**Commit message prefixes**:

- `feat:` - New feature
- `fix:` - Bug fix
- `docs:` - Documentation
- `test:` - Tests
- `refactor:` - Refactoring
- `chore:` - Other

### 5. Push and open a Pull Request

```bash
# Push the branch
git push origin feature/my-feature
```

Then open a Pull Request on GitHub.

## Developer Certificate of Origin (DCO)

To keep contributions easy for individuals and companies, we use a lightweight sign-off process.

By contributing, you agree that your work is submitted under the project license and that you have the right to submit it.

Please sign off your commits:

```bash
git commit -s
```

## Code Review

### Pull Request checklist

- [ ] Code follows existing style
- [ ] Tests are added or updated
- [ ] Documentation is updated
- [ ] No linter errors
- [ ] All tests pass
- [ ] CRD manifests regenerated (if types changed): `make manifests generate`

### Review focus

- Code quality
- Test coverage
- Performance impact
- Security impact
- Documentation completeness

## Coding Conventions

### Go guidelines

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Format with `gofmt`
- Use `golangci-lint` for quality checks

### Naming

- **Packages**: lowercase, singular
- **Types**: PascalCase
- **Functions**: PascalCase (exported), camelCase (unexported)
- **Constants**: PascalCase or UPPER_SNAKE_CASE

### Error handling

```go
// Handle errors explicitly
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### Logging

```go
// Use structured logging (log/slog)
slog.Info("VIP reconciled",
    "vip", vipName,
    "backends", len(backends),
)

slog.Error("failed to apply VIP",
    "vip", vipName,
    "error", err,
)
```

## Testing

### Add tests

- Add tests for new features.
- Add regression tests for bug fixes.
- Maintain coverage.

### Run tests

```bash
# All tests
make test

# Specific package
go test ./internal/operator/...

# v2 agent tests
go test ./internal/agent/dataplane/ ./internal/agent/routing/ ./internal/agent/store/

# Coverage
go test -coverprofile=coverage.out ./...
```

## Documentation

### Update docs

- Add docs for new features.
- Update API docs for CRD schema changes.
- Update configuration docs for config changes.
- Keep both English and Japanese versions in sync.

### Doc locations

- `docs/` - User documentation (English + Japanese pairs)
- `README.md` / `README.ja.md` - Project overview
- `SPEC.md` - Technical specification

## License

Contributions are provided under the Apache License 2.0. See [LICENSE](../LICENSE).

## Code of Conduct

- Provide constructive feedback.
- Be respectful.
- Maintain an open and inclusive community.
