package models

import (
	"gorm.io/gorm"
	"time"
)

type UserRole struct {
	ID         uint        `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint        `json:"user_id" gorm:"primaryKey"`
	RoleID     string      `json:"role_id" gorm:"primaryKey"`
	Role       Role        `json:"role" gorm:"foreignKey:RoleID;references:ID"`
	CreatedAt  time.Time   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt  time.Time   `json:"updated_at" gorm:"autoUpdateTime"`
}

func MigrateUserRole(db *gorm.DB) error {
	err := db.AutoMigrate(&UserRole{})
	if err != nil {
		return err
	}
	return nil
}