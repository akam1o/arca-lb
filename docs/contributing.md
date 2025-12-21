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

## Code Review

### Pull Request checklist

- [ ] Code follows existing style
- [ ] Tests are added or updated
- [ ] Documentation is updated
- [ ] No linter errors
- [ ] All tests pass

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
- **Constants**: UPPER_SNAKE_CASE

### Error handling

```go
// Handle errors explicitly
if err != nil {
    return fmt.Errorf("context: %w", err)
}
```

### Logging

```go
// Use structured logging
logger.WithFields(logrus.Fields{
    "vip_id": vipID,
    "error": err,
}).Error("Failed to create VIP")
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
go test ./internal/controller/api/...

# Coverage
go test -coverprofile=coverage.out ./...
```

## Documentation

### Update docs

- Add docs for new features.
- Update API docs for API changes.
- Update configuration docs for config changes.

### Doc locations

- `docs/` - User documentation
- `README.md` - Project overview
- `SPEC.md` - Specification
- `PLAN.md` - Implementation plan

## License

Contributions are provided under the MIT License.

## Code of Conduct

- Provide constructive feedback.
- Be respectful.
- Maintain an open and inclusive community.

## Questions

If you have questions, ask in GitHub Issues.

## Next Steps

- See [Development Environment](./development.md) to get started
- See [Architecture](./architecture.md) to understand the system design
