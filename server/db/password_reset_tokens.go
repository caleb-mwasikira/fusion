package db

import (
	"database/sql"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	MIN_OTP_LEN int = 6
)

type PasswordResetToken struct {
	Id        int       `json:"id"`
	Email     string    `json:"email"`
	OTP       string    `json:"otp"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func generateOtp(length int) string {
	otp := ""
	for range length {
		rand_digit := rand.IntN(10)
		otp += fmt.Sprint(rand_digit)
	}
	return otp
}

func NewPasswordResetToken(email string, duration time.Duration) *PasswordResetToken {
	now := time.Now()
	expiry_time := now.Add(duration)
	otp := generateOtp(MIN_OTP_LEN)

	return &PasswordResetToken{
		Email:     email,
		OTP:       otp,
		CreatedAt: now,
		ExpiresAt: expiry_time,
	}
}

type PasswordResetModel struct {
	db *sql.DB
}

func NewPasswordResetModel() *PasswordResetModel {
	return &PasswordResetModel{
		db: db,
	}
}

func (m *PasswordResetModel) Insert(token PasswordResetToken) (int64, error) {
	query := "INSERT INTO password_reset_tokens(email, otp, expires_at) VALUES(?, ?, ?)"
	result, err := m.db.Exec(
		query,
		token.Email,
		token.OTP,
		token.ExpiresAt,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Fetches password_reset_token by user's email
func (m *PasswordResetModel) Get(email string) (*PasswordResetToken, error) {
	query := "SELECT * FROM password_reset_tokens WHERE email = ? AND expires_at > ?"
	row := m.db.QueryRow(query, email, time.Now())

	token := PasswordResetToken{}
	err := row.Scan(
		&token.Id,
		&token.Email,
		&token.OTP,
		&token.ExpiresAt,
		&token.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &token, nil
}
