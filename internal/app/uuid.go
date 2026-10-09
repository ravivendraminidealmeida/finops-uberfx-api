package app

import "github.com/google/uuid"

type UUIDGen struct{}

func (UUIDGen) New() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}
