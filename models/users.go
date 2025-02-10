package models

import (
	"gorm.io/gorm"
	"time"
)

type User struct {
	ID        uint        `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string      `json:"name"`
	Email     string      `gorm:"unique;not null"`
	Password  string      `gorm:"not null"`
	PhoneNumber string    `json:"phone_number" gorm:"unique"`
	Token     string      `json:"token"`
	ResetToken string     `json:"reset_token"`
	Status     string     `gorm:"default:'non_active'" json:"status"`
	ActiveOn    *time.Time   `json:"active_on" gorm:"default:null"`
	LastActive  *time.Time   `json:"last_active" gorm:"default:null"`
	UserRoles []UserRole  `json:"user_roles"  gorm:"foreignKey:UserID"`
	CreatedAt time.Time   `json:"created_at"  gorm:"autoCreateTime"`
	UpdatedAt time.Time   `json:"updated_at"  gorm:"autoUpdateTime"`
}

func (u *User) IsAdmin() bool {
	for _, userRole := range u.UserRoles {
		if userRole.RoleID == "admin" {
			return true
		}
	}
	return false
}

// Implement GetID() for User
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