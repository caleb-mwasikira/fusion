package lib

import (
	"fmt"
	"net"
	"strconv"
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

func ValidateAddress(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}

	if err = validateHost(host); err != nil {
		return err
	}
	return validatePort(port)
}

func validateHost(host string) error {
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil {
		return fmt.Errorf("invalid IP address")
	}
	return nil
}

func validatePort(port string) error {
	iport, err := strconv.Atoi(port)
	if err != nil {
		return err
	}
	if iport <= 0 && iport > 65535 {
		return fmt.Errorf("invalid port range")
	}
	return nil
}
