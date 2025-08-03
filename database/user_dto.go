package database

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"strings"
)

const (
	MIN_NAME_LEN     = 4
	MIN_PASSWORD_LEN = 8
)

var (
	secretKey string
)

func init() {
	secretKey = os.Getenv("SECRET_KEY")
	if strings.TrimSpace(secretKey) == "" {
		log.Fatalln("Missing SECRET_KEY env variable")
	}
}

type User struct {
	Id       int    `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	OrgName  string `json:"org_name"`
	DeptName string `json:"dept_name"`
}

func NewUser(username, password, orgName, deptName string) (*User, error) {
	if !validLength(username, MIN_NAME_LEN) {
		return nil, fmt.Errorf("username too short")
	}
	if !validLength(password, MIN_PASSWORD_LEN) {
		return nil, fmt.Errorf("password too short")
	}
	if !validLength(orgName, 1) || !validLength(deptName, 1) {
		return nil, fmt.Errorf("orgName or deptName too short")
	}

	// Hash user password
	hash := hmac.New(sha256.New, []byte(secretKey))
	digest := hash.Sum([]byte(password))
	hashedPassword := hex.EncodeToString(digest)

	return &User{
		Username: username,
		Password: hashedPassword,
		OrgName:  orgName,
		DeptName: deptName,
	}, nil
}

func validLength(val string, minLength int) bool {
	val = strings.TrimSpace(val)
	return len(val) >= minLength
}
