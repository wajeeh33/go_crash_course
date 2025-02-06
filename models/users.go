package models

import (
	"gorm.io/gorm"
	"time"
)

type User struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string   `json:"name"`
	Email     string   `json:"email"`
	Password  string   `json:"password"`
	Books     []Book    `json:"books" gorm:"foreignKey:UserID"` // Associate books with the user
	UserRoles []UserRole `json:"user_roles" gorm:"foreignKey:UserID"`
	CreatedAt time.Time   `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time   `json:"updated_at" gorm:"autoUpdateTime"`
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