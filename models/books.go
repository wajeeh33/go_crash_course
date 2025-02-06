package models

import (
	"gorm.io/gorm"
	"time"
)

type Book struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Title     *string   `json:"title"`
	Author    *string   `json:"author"`
	Publisher *string   `json:"publisher"`
	ImagePath *string   `json:"image_path"`
	UserID    uint      `json:"user_id"` // Foreign key to associate book with a user
	CreatedAt time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}
// MigrateBook automatically migrates the Book schema
func MigrateBook(db *gorm.DB) error {
	err := db.AutoMigrate(&Book{})
	if err != nil {
		return err
	}
	return nil
}

// Implement GetID() for Book
func (b Book) GetID() uint {
	return b.ID
}