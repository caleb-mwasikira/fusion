package database

import "database/sql"

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
