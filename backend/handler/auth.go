package handler

import (
	"database/sql"
	"net/http"

	"syslog-gui/auth"

	"github.com/gin-gonic/gin"
)

type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required,min=6"`
}

func Login(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		var user auth.User
		err := db.QueryRow(
			"SELECT id, username, is_admin, created_at FROM users WHERE username = $1",
			req.Username,
		).Scan(&user.ID, &user.Username, &user.IsAdmin, &user.CreatedAt)

		if err == sql.ErrNoRows {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}

		var passwordHash string
		if err := db.QueryRow("SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&passwordHash); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
			return
		}

		if !auth.CheckPasswordHash(req.Password, passwordHash) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid credentials"})
			return
		}

		token, err := auth.GenerateToken(user)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate token"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"token":  token,
			"user":   user,
			"message": "Login successful",
		})
	}
}

func Register(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		var count int
		db.QueryRow("SELECT COUNT(*) FROM users WHERE username = $1", req.Username).Scan(&count)
		if count > 0 {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
			return
		}

		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not hash password"})
			return
		}

		result, err := db.Exec(
			"INSERT INTO users (username, password_hash, is_admin) VALUES ($1, $2, $3)",
			req.Username, hash, false,
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not create user"})
			return
		}

		id, _ := result.LastInsertId()
		user := auth.User{
			ID:       id,
			Username: req.Username,
			IsAdmin:  false,
		}

		token, _ := auth.GenerateToken(user)

		c.JSON(http.StatusCreated, gin.H{
			"token": token,
			"user":  user,
		})
	}
}

func GetMe() gin.HandlerFunc {
	return func(c *gin.Context) {
		user := auth.GetUserFromContext(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
			return
		}
		c.JSON(http.StatusOK, user)
	}
}

func ChangePassword(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		type req struct {
			OldPassword string `json:"old_password" binding:"required"`
			NewPassword string `json:"new_password" binding:"required,min=6"`
		}

		var r req
		if err := c.ShouldBindJSON(&r); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
			return
		}

		user := auth.GetUserFromContext(c)
		if user == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
			return
		}

		var passwordHash string
		db.QueryRow("SELECT password_hash FROM users WHERE id = $1", user.ID).Scan(&passwordHash)

		if !auth.CheckPasswordHash(r.OldPassword, passwordHash) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid old password"})
			return
		}

		newHash, err := auth.HashPassword(r.NewPassword)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not hash password"})
			return
		}

		db.Exec("UPDATE users SET password_hash = $1 WHERE id = $2", newHash, user.ID)
		c.JSON(http.StatusOK, gin.H{"message": "Password changed successfully"})
	}
}
