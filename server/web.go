package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/caleb-mwasikira/fusion/server/auth"
	"github.com/caleb-mwasikira/fusion/server/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var (
	users               *db.UserModel          = db.NewUserModel()
	passwordResetTokens *db.PasswordResetModel = db.NewPasswordResetModel()
	organizations       *db.OrganizationModel  = db.NewOrganizationModel()
)

func jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	OrgName  string `json:"org_name"`
	DeptName string `json:"dept_name"`
}

func (req registerRequest) Validate() error {
	if err := lib.ValidateName("username", req.Username); err != nil {
		return err
	}
	if err := lib.ValidateEmail(req.Email); err != nil {
		return err
	}
	if err := lib.ValidatePassword(req.Password); err != nil {
		return err
	}
	if err := lib.ValidateName("orgName", req.OrgName); err != nil {
		return err
	}
	if err := lib.ValidateName("deptName", req.DeptName); err != nil {
		return err
	}
	return nil
}

func registerHandler(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "username, email, password, org_name, dept_name fields required"})
		return
	}

	err = req.Validate()
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	ok, _ := users.Exists(req.Email)
	if ok {
		jsonResponse(w, http.StatusConflict, map[string]string{"message": "user account already exists"})
		return
	}

	// Verify that users orgName and deptName exist
	baseDir := filepath.Join(realpath, req.OrgName, req.DeptName)
	if !dirExists(baseDir) {
		errMessage := fmt.Sprintf("Organization '%v' with department '%v' NOT found", req.OrgName, req.DeptName)
		jsonResponse(w, http.StatusNotFound, map[string]string{"message": errMessage})
		return
	}

	user, err := db.NewUser(
		req.Username,
		req.Email,
		req.Password,
		req.OrgName,
		req.DeptName,
	)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	_, err = users.Insert(*user)
	if err != nil {
		log.Printf("Error creating user account; %v\n", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "error creating user account"})
		return
	}

	jsonResponse(w, http.StatusCreated, map[string]string{"message": "user registered"})
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (req loginRequest) Validate() error {
	return lib.ValidateEmail(req.Email)
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "username, password fields required"})
		return
	}

	err = req.Validate()
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	user, err := users.Get(req.Email)
	if err != nil {
		log.Printf("Error fetching user account; %v\n", err)
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "invalid username or password"})
		return
	}

	passwordMatch := auth.VerifyPassword(user.Password, req.Password)
	if !passwordMatch {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "invalid username or password"})
		return
	}

	accessToken, err := auth.GenerateToken(*user)
	if err != nil {
		log.Printf("Error generating JWT; %v\n", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "error logging in user"})
		return
	}

	// Return JWT to user
	jsonResponse(w, http.StatusOK, map[string]string{
		"message":      "Login successful",
		"access_token": accessToken,
	})
}

type createOrgRequest struct {
	OrgName     string `json:"org_name"`
	DeptName    string `json:"dept_name"`
	OrgPassword string `json:"org_password"`
}

func (req createOrgRequest) Validate() error {
	// No need to validate department name
	return lib.ValidateName("orgName", req.OrgName)
}

func createOrgHandler(w http.ResponseWriter, r *http.Request) {
	// Fetch user value handed down from context
	userObj := r.Context().Value(auth.USER_CTX_KEY)
	user, ok := userObj.(*db.User)
	if !ok {
		log.Println("Error extracting user object from context")
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "error fetching current logged in user"})
		return
	}

	var req createOrgRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "org_name and dept_name fields required"})
		return
	}

	err = req.Validate()
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "org_name field required"})
		return
	}

	// Check if organization directory already exists
	orgDir := filepath.Join(realpath, req.OrgName)
	org, err := db.NewOrganization(
		orgDir,
		req.DeptName,
		user.Username,
		user.Email,
		req.OrgPassword,
	)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}

	// Save organization record to database
	_, err = organizations.Insert(*org)
	if err != nil {
		// Rollback directory creation
		os.RemoveAll(orgDir)

		log.Printf("error creating organization; %v\n", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "error creating organization"})
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"message": "organization and department directory created successfully"})
}

func sendEmail(email, otp string) error {
	err := lib.LoadEnv()
	if err != nil {
		return fmt.Errorf("error loading env file; %v", err)
	}

	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	from := os.Getenv("SMTP_EMAIL")
	password := os.Getenv("SMTP_PASSWORD") // App Password (not actual Gmail password)
	to := []string{email}

	message := []byte(
		"Subject: Reset your password\r\n" +
			"MIME-version: 1.0;\r\n" +
			"Content-Type: text/html; charset=\"UTF-8\";\r\n" +
			"\r\n" +
			"<html>" +
			"<body style='font-family: Arial, sans-serif;'>" +
			"<h2>Password Reset Request</h2>" +
			"<p>Hello, there</p>" +
			"<p>We received a request to reset your password on your File Manager account. Use the following One-Time Password (OTP) to continue:</p>" +
			"<div style='font-size: 24px; font-weight: bold; background:#f4f4f4; padding:10px; border-radius:5px; display:inline-block;'>" + otp + "</div>" +
			"<p>This code will expire in <b>10 minutes</b>.</p>" +
			"<p>If you didn't request a password reset, you can safely ignore this email.</p>" +
			"<br>" +
			"<p>Best regards,<br>File Manager</p>" +
			"</body>" +
			"</html>",
	)

	auth := smtp.PlainAuth("", from, password, smtpHost)
	addr := net.JoinHostPort(smtpHost, smtpPort)
	err = smtp.SendMail(addr, auth, from, to, message)
	if err != nil {
		log.Fatal(err)
	}
	return nil
}

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

func (req forgotPasswordRequest) Validate() error {
	if err := lib.ValidateEmail(req.Email); err != nil {
		return err
	}
	return nil
}

func forgotPasswordHandler(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "email field required"})
		return
	}

	// validate email
	err = req.Validate()
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	// if user does not exist we still send a 200 OK response.
	// this is done to prevent people from searching emails registered with
	// the system via this route
	ok, _ := users.Exists(req.Email)
	if !ok {
		log.Printf("Error fetching user account; %v\n", err)
		jsonResponse(w, http.StatusOK, map[string]string{"message": "password reset token has been sent to your email"})
		return
	}

	// create password reset token
	token := db.NewPasswordResetToken(req.Email, 72*time.Hour)
	_, err = passwordResetTokens.Insert(*token)
	if err != nil {
		log.Printf("Error saving password_reset_token; %v\n", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "error creating password reset token"})
		return
	}

	// Send password reset token to user's email
	go func(email, otp string) {
		err := sendEmail(email, otp)
		if err != nil {
			log.Printf("Error sending email; %v\n", err)
		}
	}(req.Email, token.OTP)

	jsonResponse(w, http.StatusOK, map[string]string{"message": "password reset token has been sent to your email"})
}

type passwordResetRequest struct {
	Email       string `json:"email"`
	OTP         string `json:"otp"`
	NewPassword string `json:"new_password"`
}

func (req passwordResetRequest) Validate() error {
	if err := lib.ValidateEmail(req.Email); err != nil {
		return err
	}
	if err := lib.ValidatePassword(req.NewPassword); err != nil {
		return err
	}
	return nil
}

func resetPasswordHandler(w http.ResponseWriter, r *http.Request) {
	var req passwordResetRequest
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&req)
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "email, otp and new_password fields required"})
		return
	}

	err = req.Validate()
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}

	token, err := passwordResetTokens.Get(req.Email, req.OTP)
	if err != nil {
		log.Printf("Error fetching password_reset_token; %v\n", err)
		jsonResponse(w, http.StatusNotFound, map[string]string{"message": "invalid or expired OTP"})
		return
	}

	// verify database otp matches one passed in by user
	if token.OTP != req.OTP {
		jsonResponse(w, http.StatusNotFound, map[string]string{"message": "invalid or expired OTP"})
		return
	}

	// change users password
	_, err = users.ChangePassword(req.Email, req.NewPassword)
	if err != nil {
		log.Printf("Error changing user password; %v\n", err)
		jsonResponse(w, http.StatusInternalServerError, map[string]string{"message": "invalid or expired OTP"})
		return
	}

	// Invalidate OTP to prevent re-use
	go passwordResetTokens.Delete(req.Email, req.OTP)

	jsonResponse(w, http.StatusOK, map[string]string{"message": "Password reset successful"})
}

func requireAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		fields := strings.Split(authHeader, " ")
		if len(fields) != 2 {
			jsonResponse(w, http.StatusBadRequest, map[string]string{"message": "invalid Authorization header format"})
			return
		}

		token := fields[1]
		var user db.User
		if !auth.ValidToken(token, &user) {
			jsonResponse(w, http.StatusUnauthorized, map[string]string{"message": "access to this route requried user login."})
			return
		}

		// embed user into context
		newCtx := context.WithValue(r.Context(), auth.USER_CTX_KEY, &user)

		next.ServeHTTP(w, r.WithContext(newCtx))
	})
}

// We are going to move some functionality from gRPC into
// a HTTP web server
func startWebServer(doneChan chan<- error) {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Post("/auth/register", registerHandler)
	r.Post("/auth/login", loginHandler)
	r.Post("/auth/forgot-password", forgotPasswordHandler)
	r.Post("/auth/reset-password", resetPasswordHandler)

	r.Group(func(r chi.Router) {
		r.Use(requireAuthMiddleware)

		// Anyone can create an organization so long as they are logged in
		r.Get("/create-organization", createOrgHandler)
	})

	address := "127.0.0.1:5000"
	log.Printf("Starting web server on http://%v\n", address)
	err := http.ListenAndServe(address, r)
	if err != nil {
		doneChan <- err
	}
}
