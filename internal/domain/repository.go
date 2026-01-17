package domain

import (
	"context"
	"errors"
)

type FunctionRepository interface {
	// Save stores a new function or updates an existing one
	Save(ctx context.Context, function *Function) error

	// FindByID retrieves a function by its unique identifier
	FindByID(ctx context.Context, id string) (*Function, error)

	// FindByName retrieves a function by its name
	FindByName(ctx context.Context, name string) (*Function, error)

	// List retrieves a list of functions with pagination
	List(ctx context.Context, offset, limit int) ([]*Function, error)

	// Delete removes a function by its unique identifier
	Delete(ctx context.Context, id string) error

	// Count returns the total number of functions
	Count(ctx context.Context) (int64, error)

	// Exists checks if a function with the given identifier exists
	Exists(ctx context.Context, id string) (bool, error)
}

type CodeStorage interface {
	Store(ctx context.Context, functionID string, code []byte) (string, error)

	Retrieve(ctx context.Context, key string) ([]byte, error)

	Delete(ctx context.Context, key string) error

	Exists(ctx context.Context, key string) (bool, error)
}

var (
	ErrFunctionExists     = errors.New("function already exists")
	ErrInvalidFunctionID  = errors.New("invalid function id")
	ErrStorageUnavailable = errors.New("storage unavailable")
	ErrCodeNotFound       = errors.New("function code not found")
)

type FunctionService struct {
	repo    FunctionRepository
	storage CodeStorage
}

func NewFunctionService(
	repo FunctionRepository,
	storage CodeStorage,
) *FunctionService {
	return &FunctionService{
		repo:    repo,
		storage: storage,
	}
}

func (s *FunctionService) CreateFunction(
	ctx context.Context,
	function *Function,
) error {
	if err := function.Validate(); err != nil {
		return err
	}

	existing, err := s.repo.FindByName(ctx, function.Name)
	if err != nil && err != ErrFunctionNotFound {
		return err
	}

	if existing != nil {
		return ErrFunctionExists
	}

	codeKey, err := s.storage.Store(ctx, function.ID, function.Code)
	if err != nil {
		return err
	}

	functionCopy := *function
	functionCopy.Code = []byte(codeKey)

	if err := s.repo.Save(ctx, &functionCopy); err != nil {
		// Rollback: Delete the stored code if DB save fails
		s.storage.Delete(ctx, codeKey)
		return err
	}

	return nil
}

func (s *FunctionService) GetFunction(
	ctx context.Context,
	id string,
) (*Function, error) {
	// Get metadata from repository
	function, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Load code from storage
	codeKey := string(function.Code)
	code, err := s.storage.Retrieve((ctx), codeKey)
	if err != nil {
		return nil, err
	}

	function.Code = code

	return function, nil
}

func (s *FunctionService) UpdateFunction(
	ctx context.Context,
	function *Function,
) error {
	if err := function.Validate(); err != nil {
		return err
	}

	// Check if function exists
	existing, err := s.repo.FindByID(ctx, function.ID)
	if err != nil {
		return err
	}

	// If code changed, update storage
	oldCodeKey := string(existing.Code)
	newCodeKey := oldCodeKey

	if len(function.Code) > 0 {
		newCodeKey, err = s.storage.Store(ctx, function.ID, function.Code)
		if err != nil {
			return err
		}

		// Delete old code (cleanup)
		if oldCodeKey != newCodeKey {
			s.storage.Delete(ctx, oldCodeKey)
		}
	}

	// Update metadata
	functionCopy := *function
	functionCopy.Code = []byte(newCodeKey)

	return s.repo.Save(ctx, &functionCopy)
}

func (s *FunctionService) DeleteFunction(ctx context.Context, id string) error {
	function, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	codeKey := string(function.Code)

	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	s.storage.Delete(ctx, codeKey)

	return nil
}

func (s *FunctionService) ListFunctions(
	ctx context.Context,
	offset, limit int,
) ([]*Function, error) {
	return s.repo.List(ctx, offset, limit)
}
