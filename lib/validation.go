package lib

import (
	"fmt"
	"strings"
)

const (
	MIN_NAME_LEN     = 4
	MIN_PASSWORD_LEN = 8
)

func ValidateName(field, name string) error {
	name = strings.TrimSpace(name)
	if len(name) < MIN_NAME_LEN {
		return fmt.Errorf("%v too short", field)
	}
	return nil
}

func ValidateEmail(email string) error {
	if !strings.Contains(email, "@") || !strings.Contains(email, ".com") {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func ValidatePassword(password string) error {
	password = strings.TrimSpace(password)
	if len(password) < MIN_PASSWORD_LEN {
		return fmt.Errorf("password too short")
	}

	// TODO: check password strength
	return nil
}
