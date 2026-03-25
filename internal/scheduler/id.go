package scheduler

import (
	"fmt"

	"github.com/google/uuid"
)

func generateID(prefix string) string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(fmt.Errorf("generate uuidv7: %w", err))
	}
	return fmt.Sprintf("%s_%s", prefix, id.String())
}
