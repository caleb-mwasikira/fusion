package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Organization struct {
	Name        string `json:"name"`
	AdminName   string `json:"admin_name"`
	AdminEmail  string `json:"admin_email"`
	OrgPassword string `json:"org_password"`
}

// Validates user details and creates a new organization.
// Does password hashing, you can pass in the password as plaintext
func NewOrganization(
	orgDir string,
	deptName string,
	adminName string,
	adminEmail string,
	orgPassword string,
) (*Organization, error) {
	// Check if organization directory already exists
	_, err := os.Stat(orgDir)
	if err == nil {
		return nil, fmt.Errorf("organization already exists")
	}

	// Create organization directory
	err = os.MkdirAll(orgDir, 0751)
	if err != nil {
		return nil, fmt.Errorf("error creating organization directory")
	}

	isEmpty := func(value string) bool {
		return strings.TrimSpace(value) == ""
	}
	if !isEmpty(deptName) {
		// Create department directory
		deptDir := filepath.Join(orgDir, deptName)
		err := os.MkdirAll(deptDir, 0771)
		if err != nil {
			return nil, fmt.Errorf("error creating department directory")
		}
	}

	return &Organization{
		Name:        filepath.Base(orgDir),
		AdminName:   adminName,
		AdminEmail:  adminEmail,
		OrgPassword: hashPassword(orgPassword),
	}, nil
}

type OrganizationModel struct {
	db *sql.DB
}

func NewOrganizationModel() *OrganizationModel {
	return &OrganizationModel{
		db: db,
	}
}

func (m *OrganizationModel) Insert(o Organization) (int64, error) {
	query := "INSERT INTO organizations(name, admin_name, admin_email, org_password) VALUES(?, ?, ?, ?)"
	result, err := m.db.Exec(
		query,
		o.Name,
		o.AdminName,
		o.AdminEmail,
		o.OrgPassword,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
