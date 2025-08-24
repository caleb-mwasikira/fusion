package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/caleb-mwasikira/fusion/lib"
	"github.com/caleb-mwasikira/fusion/server/db"
	"github.com/golang-jwt/jwt/v5"
)

var (
	SECRET_KEY string
)

func init() {
	err := lib.LoadEnv()
	if err != nil {
		log.Fatalf("Error loading environment variables; %v\n", err)
	}

	SECRET_KEY = os.Getenv("SECRET_KEY")
	if strings.TrimSpace(SECRET_KEY) == "" {
		log.Fatalln("Missing SECRET_KEY environment variable")
	}
}

func GenerateToken(user db.User) (string, error) {
	data, err := json.Marshal(user)
	if err != nil {
		return "", err
	}

	b64EncodedData := base64.StdEncoding.EncodeToString(data)
	now := time.Now()
	expiry := now.Add(72 * time.Hour)

	token := jwt.NewWithClaims(
		jwt.SigningMethodHS256,
		jwt.MapClaims{
			"iat": now.Unix(),
			"exp": expiry.Unix(),
			"iss": "fusion",
			"sub": b64EncodedData,
		},
	)
	tokenString, err := token.SignedString([]byte(SECRET_KEY))
	return tokenString, err
}

// Verifies a json web token and returns the object stored
// in "sub" subject field. expects obj parameter to be a pointer of type T
func ValidToken(tokenString string, obj any) bool {
	token, err := jwt.Parse(
		tokenString,
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(SECRET_KEY), nil
		},
	)
	if err != nil {
		log.Printf("Error parsing jwt; %v\n", err)
		return false
	}

	// read claims from payload and extract the b64 encoded data.
	claims, ok := token.Claims.(jwt.MapClaims)
	if ok && token.Valid {
		// get subject - stored as base64 data
		b64EncodedData, ok := claims["sub"].(string)
		if !ok {
			log.Println("Unexpected \"sub\" type in jwt")
			return false
		}

		// decode data
		data, err := base64.StdEncoding.DecodeString(b64EncodedData)
		if err != nil {
			log.Printf("Error decoding \"sub\" value of jwt; %v\n", err)
			return false
		}

		// unmarshal the data into the param object
		err = json.Unmarshal(data, obj)
		return err == nil
	}

	return false
}

// func hashUserPassword(password string) string {
// 	hash := hmac.New(sha256.New, []byte(SECRET_KEY))
// 	digest := hash.Sum([]byte(password))
// 	return fmt.Sprintf("%x", digest)
// }

func VerifyPassword(dbPassword, password string) bool {
	hash := hmac.New(sha256.New, []byte(SECRET_KEY))
	mac2 := hash.Sum([]byte(password))
	hmacPassword, err := hex.DecodeString(dbPassword)
	if err != nil {
		return false
	}
	return hmac.Equal([]byte(hmacPassword), mac2)
}
