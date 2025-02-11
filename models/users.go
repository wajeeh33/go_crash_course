package models

import (
	"gorm.io/gorm"
	"time"
)

type User struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id" schema:"-"`
	Name        string    `json:"name" schema:"name"`
	Email       string    `gorm:"unique;not null" json:"email" schema:"email"`
	Password    string    `gorm:"not null" json:"password" schema:"password"`
	PhoneNumber string    `json:"phone_number" schema:"phone_number" gorm:"unique"`
	Token       string    `json:"token" schema:"-"`
	ResetToken  string    `json:"reset_token" schema:"-"`
	Status      string    `gorm:"default:'non_active'" json:"status" schema:"status"`
	ImagePath   *string    `json:"image_path" schema:"image_path"`
	ActiveOn    *time.Time `json:"active_on" gorm:"default:null" schema:"-"`
	LastActive  *time.Time `json:"last_active" gorm:"default:null" schema:"-"`
	UserRoles   []UserRole `json:"user_roles" gorm:"foreignKey:UserID" schema:"-"`
	CreatedAt   time.Time `json:"created_at" gorm:"autoCreateTime" schema:"-"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"autoUpdateTime" schema:"-"`
}

func (u *User) IsAdmin() bool {
	for _, userRole := range u.UserRoles {
		if userRole.RoleID == "admin" {
			return true
		}
	}
	return false
}

// GetID Implement for User
func (u User) GetID() uint {
	return u.ID
}


func MigrateUser(db *gorm.DB) error {
	err := db.AutoMigrate(&User{})
	if err != nil {
		return err
	}
	return nil
}