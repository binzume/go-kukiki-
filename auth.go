package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const DefaultSessionTTL = 86400 * 365

type AccountConfig struct {
	Name     string `yaml:"name"`
	AuthType string `yaml:"auth_type"`
	Secret   string `yaml:"secret"`
}

type AuthConfig struct {
	Accounts      []AccountConfig `toml:"accounts" yaml:"accounts"`
	GuestAccount  string          `yaml:"guest_account"`
	SessionSecret string          `yaml:"session_secret"`
	SessionTTL    int             `yaml:"sessionTTL"`
}

func LoadAuthConfig(fname string) (*AuthConfig, error) {
	var c AuthConfig
	_, err := toml.DecodeFile(fname, &c)
	if err != nil {
		return &AuthConfig{}, err
	}
	return &c, err
}

func (c *AuthConfig) VerifyPassword(user, password string) (*AccountConfig, error) {
	a, err := c.getAccountByName(user)
	if err != nil {
		return nil, err
	}
	if !checkPassword(password, a) {
		return nil, errors.New("invalid password")
	}
	return a, nil
}

func (c *AuthConfig) VerifySession(sessionStr string) (*AccountConfig, error) {
	return getAccountFromSessionString(sessionStr, c)
}

func (c *AuthConfig) CreateSessionString(a *AccountConfig) string {
	return genSessionString(a.Name, c)
}

func (c *AuthConfig) sessionTTLSeconds() int64 {
	if c.SessionTTL <= 0 {
		return int64(DefaultSessionTTL)
	}
	return int64(c.SessionTTL)
}

func (c *AuthConfig) VerifyToken(token string) (*AccountConfig, error) {
	a, err := c.getAccountByToken(token)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (conf *AuthConfig) getAccountByName(name string) (*AccountConfig, error) {
	for _, a := range conf.Accounts {
		if a.Name == name {
			return &a, nil
		}
	}
	return nil, errors.New("account not found")
}

func (conf *AuthConfig) getAccountByToken(token string) (*AccountConfig, error) {
	for _, a := range conf.Accounts {
		if a.AuthType == "token" && token == a.Secret {
			return &a, nil
		}
	}
	return nil, errors.New("account not found")
}

func genRandom(n int) []byte {
	const letters = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-_"
	b := make([]byte, n)
	r := make([]byte, n)
	if _, err := rand.Read(r); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = letters[r[i]%byte(len(letters))]
	}
	return b
}

func logInsecureAccount(password string) {
	hmacSecret := genRandom(8)
	mac := hmac.New(sha1.New, hmacSecret)
	mac.Write([]byte(password))
	log.Println("Inseure account setting. TODO: use hmac string: " + string(hmacSecret) + ":" + hex.EncodeToString(mac.Sum(nil)))
}

func sha1hash(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func checkPassword(password string, a *AccountConfig) bool {
	switch a.AuthType {
	case "INSECURE":
		if password == a.Secret {
			logInsecureAccount(password)
			return true
		}
	case "password":
		s := strings.SplitN(a.Secret, ":", 2)
		if len(s) == 2 {
			mac := hmac.New(sha1.New, []byte(s[0]))
			mac.Write([]byte(password))
			return hex.EncodeToString(mac.Sum(nil)) == s[1]
		}
	}
	return false
}

func genSessionString(id string, conf *AuthConfig) string {
	return genSessionStringAt(id, time.Now().Unix(), conf)
}

func genSessionStringAt(id string, issuedAt int64, conf *AuthConfig) string {
	payload := id + ":" + strconv.FormatInt(issuedAt, 10)
	mac := hmac.New(sha1.New, []byte(conf.SessionSecret))
	mac.Write([]byte(payload))
	return payload + ":" + hex.EncodeToString(mac.Sum(nil))
}

func parseSessionString(sessionStr string, conf *AuthConfig) (id string, err error) {
	s := strings.SplitN(sessionStr, ":", 3)
	if len(s) != 3 {
		return "", errors.New("invalidSession")
	}
	issuedAt, parseErr := strconv.ParseInt(s[1], 10, 64)
	if parseErr != nil {
		return "", errors.New("invalidSession")
	}
	payload := s[0] + ":" + s[1]
	mac := hmac.New(sha1.New, []byte(conf.SessionSecret))
	mac.Write([]byte(payload))
	providedMAC, decodeErr := hex.DecodeString(s[2])
	if decodeErr != nil || !hmac.Equal(mac.Sum(nil), providedMAC) {
		return "", errors.New("invalidSession")
	}

	now := time.Now().Unix()
	ttl := conf.sessionTTLSeconds()
	if issuedAt > now || issuedAt <= now-ttl {
		return "", errors.New("invalidSession")
	}
	return s[0], nil
}

func getAccountFromSessionString(sessionStr string, conf *AuthConfig) (*AccountConfig, error) {
	user, err := parseSessionString(sessionStr, conf)
	if err != nil {
		return nil, err
	}
	return conf.getAccountByName(user)
}
