package models

import (
	"gorm.io/gorm"
	"time"
)

type Role struct {
	ID           string      `gorm:"primaryKey" json:"id"`
	Title       *string      `json:"title"`
	CreatedAt   time.Time    `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time    `json:"updated_at" gorm:"autoUpdateTime"`
}

func MigrateRole(db *gorm.DB) error {
	err := db.AutoMigrate(&Role{})
	if err != nil {
		return err
	}
	return nil
}