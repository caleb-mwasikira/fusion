package db

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	MIN_NAME_LEN     = 4
	MIN_PASSWORD_LEN = 8
)

type User struct {
	Id       int    `json:"id"`
	Username string `json:"username"`
	Password string `json:"password"`
	OrgName  string `json:"org_name"`
	DeptName string `json:"dept_name"`
}

func NewUser(
	username, password,
	orgName, deptName string,
	secretKey string,
) (*User, error) {
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

type UserModel struct {
	db *sql.DB
}

func NewUserModel() *UserModel {
	return &UserModel{
		db: db,
	}
}

// Saves a user instance onto the database.
//
//	!! Make sure you create your user with NewUser() inorder
//	for it to do field validation and password hashing
func (m *UserModel) Insert(user User) (int64, error) {
	query := "INSERT INTO users(username, password, org_name, dept_name) VALUES(?, ?, ?, ?)"
	result, err := m.db.Exec(
		query,
		user.Username,
		user.Password,
		user.OrgName,
		user.DeptName,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (m *UserModel) Get(username string) (*User, error) {
	query := "SELECT * FROM users WHERE username = ?"
	row := m.db.QueryRow(query, username)

	user := User{}
	err := row.Scan(
		&user.Id,
		&user.Username,
		&user.Password,
		&user.OrgName,
		&user.DeptName,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}
