package handler

import (
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"

	"database/sql"

	"logmara/audit"
	"logmara/middleware"
	"logmara/model"

	"github.com/gin-gonic/gin"
)

func UploadSSLCerts(database *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		certFile, certHeader, err := c.Request.FormFile("cert")
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("Certificate file is required", nil))
			return
		}
		defer certFile.Close()

		keyFile, keyHeader, err := c.Request.FormFile("key")
		if err != nil {
			middleware.HandleError(c, model.NewBadRequest("Key file is required", nil))
			return
		}
		defer keyFile.Close()

		if certHeader.Size > 5*1024*1024 || keyHeader.Size > 5*1024*1024 {
			middleware.HandleError(c, model.NewBadRequest("File too large (max 5 MB)", nil))
			return
		}

		sslDir := os.Getenv("SSL_DIR")
		if sslDir == "" {
			sslDir = "/data/ssl"
		}

		if err := os.MkdirAll(sslDir, 0700); err != nil {
			slog.Error("failed to create SSL directory", "dir", sslDir, "error", err)
			middleware.HandleError(c, model.NewInternal("Failed to create SSL directory", err))
			return
		}

		certPath := filepath.Join(sslDir, "server.crt")
		keyPath := filepath.Join(sslDir, "server.key")

		if err := saveUploadedFile(certFile, certPath); err != nil {
			slog.Error("failed to save cert", "error", err)
			middleware.HandleError(c, model.NewInternal("Failed to save certificate", err))
			return
		}

		if err := saveUploadedFile(keyFile, keyPath); err != nil {
			slog.Error("failed to save key", "error", err)
			middleware.HandleError(c, model.NewInternal("Failed to save key", err))
			return
		}

		os.Chmod(certPath, 0644)
		os.Chmod(keyPath, 0600)

		slog.Info("SSL certificates uploaded", "cert", certPath, "key", keyPath)

		actorID, actorName := actorFromContext(c)
		audit.LogAudit(database, actorID, actorName, "ssl_cert_uploaded", c.ClientIP(), "")

		certMeta := parseCertMetadata(certPath)

		c.JSON(http.StatusOK, gin.H{
			"message":   "SSL certificates uploaded successfully",
			"cert_path": certPath,
			"key_path":  keyPath,
			"cert_info": certMeta,
		})
	}
}

func saveUploadedFile(file multipart.File, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, file)
	return err
}

func parseCertMetadata(path string) map[string]interface{} {
	result := make(map[string]interface{})
	data, err := os.ReadFile(path)
	if err != nil {
		result["error"] = "failed to read certificate"
		return result
	}
	block, _ := pem.Decode(data)
	if block == nil {
		result["error"] = "invalid PEM format"
		return result
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		result["error"] = "failed to parse certificate"
		return result
	}
	result["subject"] = cert.Subject.CommonName
	result["issuer"] = cert.Issuer.CommonName
	result["valid_from"] = cert.NotBefore.Format("2006-01-02 15:04:05")
	result["valid_to"] = cert.NotAfter.Format("2006-01-02 15:04:05")
	result["dns_names"] = cert.DNSNames
	return result
}
