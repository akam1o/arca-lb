package datastore

import "errors"

var (
	// ErrNotFound is returned when a resource is not found
	ErrNotFound = errors.New("resource not found")

	// ErrConflict is returned when a resource already exists
	ErrConflict = errors.New("resource conflict")

	// ErrInvalidInput is returned when input validation fails
	ErrInvalidInput = errors.New("invalid input")
)
