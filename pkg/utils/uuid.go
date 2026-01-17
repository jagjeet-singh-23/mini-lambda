package utils

import (
	"github.com/gofrs/uuid/v5"
)

func GenerateID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}

	return id.String()
}
