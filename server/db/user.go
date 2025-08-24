package db

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/caleb-mwasikira/fusion/lib"
)

type User struct {
	Id       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	OrgName  string `json:"org_name"`
	DeptName string `json:"dept_name"`
}

func hashPassword(password string) string {
	hash := hmac.New(sha256.New, []byte(SECRET_KEY))
	digest := hash.Sum([]byte(password))
	return hex.EncodeToString(digest)
}

// Validates user details and creates a new user.
// Does password hashing, you can pass in the password as plaintext
func NewUser(
	username string,
	email string,
	password string,
	orgName string,
	deptName string,
) (*User, error) {
	if err := lib.ValidateName("username", username); err != nil {
		return nil, err
	}
	if err := lib.ValidateEmail(email); err != nil {
		return nil, err
	}
	if err := lib.ValidatePassword(password); err != nil {
		return nil, err
	}
	if err := lib.ValidateName("orgName", orgName); err != nil {
		return nil, err
	}
	if err := lib.ValidateName("deptName", deptName); err != nil {
		return nil, err
	}

	return &User{
		Username: username,
		Email:    email,
		Password: hashPassword(password),
		OrgName:  orgName,
		DeptName: deptName,
	}, nil
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
	query := "INSERT INTO users(username, email, password, org_name, dept_name) VALUES(?, ?, ?, ?, ?)"
	result, err := m.db.Exec(
		query,
		user.Username,
		user.Email,
		user.Password,
		user.OrgName,
		user.DeptName,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Fetches user by their email
func (m *UserModel) Get(email string) (*User, error) {
	query := "SELECT * FROM users WHERE email = ?"
	row := m.db.QueryRow(query, email)

	user := User{}
	err := row.Scan(
		&user.Id,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.OrgName,
		&user.DeptName,
	)
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (m *UserModel) Exists(email string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM users WHERE email = ?)`
	err := db.QueryRow(query, email).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// Changes a user's password. Hashes the password for you; you can pass
// in the password as plaintext
func (m *UserModel) ChangePassword(email string, newPassword string) (int64, error) {
	query := "UPDATE users SET password = ? WHERE email = ?"
	result, err := m.db.Exec(
		query,
		hashPassword(newPassword),
		email,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
