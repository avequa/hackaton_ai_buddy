// Package authn - имитация корпоративного IdP: выпуск и проверка JWT.
// В Авито авторизация уже есть; здесь важен контракт: в токене id, роль, команда, а аудитория разделяет права.
package authn

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
)

// Аудитории токенов. ui - сессия человека в интерфейсе: может подтверждать заявки.
// mcp - короткий токен, который BFF выдает агенту на один ход диалога: только инструменты MCP, без подтверждения.
const (
	AudienceUI  = "ui"
	AudienceMCP = "mcp"
	issuerName  = "ai-it-buddy-mock-idp"
)

// ErrInvalidToken - токен не подписан нами, просрочен или выдан для другой аудитории
var ErrInvalidToken = errors.New("invalid token")

// Claims - что сервер знает о пользователе из токена
type Claims struct {
	jwt.RegisteredClaims
	Name string `json:"name"`
	Role string `json:"role"`
	Team string `json:"team"`
}

// Issuer - выпускает и проверяет токены (HS256 с общим секретом)
type Issuer struct {
	secret []byte
	now    func() time.Time
}

// NewIssuer - секрет должен быть не короче 32 байт
func NewIssuer(secret string, now func() time.Time) (*Issuer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("JWT secret must be at least 32 bytes, got %d", len(secret))
	}
	if now == nil {
		now = time.Now
	}
	return &Issuer{secret: []byte(secret), now: now}, nil
}

// Issue - токен пользователя для аудитории на ttl
func (i *Issuer) Issue(u domain.User, audience string, ttl time.Duration) (string, error) {
	now := i.now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuerName,
			Subject:   u.ID,
			Audience:  jwt.ClaimStrings{audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Name: u.Name, Role: u.Role, Team: u.Team,
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return token, nil
}

// Verify - подпись, срок, издатель и аудитория
func (i *Issuer) Verify(token, audience string) (Claims, error) {
	var claims Claims
	_, err := jwt.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuerName),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	return claims, nil
}
