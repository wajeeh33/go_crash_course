package models

import (
	"gorm.io/gorm"
	"time"
)

type Book struct {
	ID          uint   `gorm:"primary key;autoincrement" json:"id"`
	Title      *string `json:"title"`
	Author     *string `json:"author"`
	Publisher  *string `json:"publisher"`
	ImagePath  *string `json:"image_path"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoCreateTime"`
}

func MigrateBook(db *gorm.DB) error {
	err := db.AutoMigrate(&Book{})
	if err != nil {
		return err
	}
	return nil
}