package main

import (
	"context"
	"encoding/hex"
	"encoding/csv"
	"crypto/rand"
	"github.com/tealeg/xlsx"
	"io"
	"log"
	"encoding/json"
    "github.com/golang-jwt/jwt/v5"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
    "fmt"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/gorilla/schema"
	"golang.org/x/crypto/bcrypt"
	"math"
	"net/http"
	"gorm.io/gorm"
	"github.com/wajeeh33/go_crash_course/storage"
	"github.com/wajeeh33/go_crash_course/models"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"sort"
)

const MaxFileSize = 10 * 1024 * 1024 // 10 MB

type Repository struct {
	DB *gorm.DB
}

type HasID interface {
	GetID() uint
}

// SortByID Generic function to sort slices by ID
func SortByID[T HasID](items []T) {
	sort.Slice(items, func(i, j int) bool {
		return items[i].GetID() > items[j].GetID() // Sort in ascending order
	})
}

// Helper function to write JSON responses
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (r *Repository) GetBooks(w http.ResponseWriter, req *http.Request) {
	bookModels := []models.Book{}
	query := r.DB

	// Read query parameters
	author := req.URL.Query().Get("author")
	title := req.URL.Query().Get("title")
	publisher := req.URL.Query().Get("publisher")
	search := req.URL.Query().Get("search")

	// Check for spaces in the author name
	if author != "" {
		trimmedAuthor := strings.TrimSpace(author)
		authorParts := strings.Fields(trimmedAuthor)

		if len(authorParts) == 0 {
			http.Error(w, "Author name cannot be empty", http.StatusBadRequest)
			return
		}

		// Prepare the query for both first and last names
		query = query.Where("LOWER(author) LIKE ?", "%"+strings.ToLower(trimmedAuthor)+"%")
	}

	// Check for spaces in the title
	if title != "" {
		trimmedTitle := strings.TrimSpace(title)
		TitleParts := strings.Fields(trimmedTitle)

		if len(TitleParts) == 0 {
			http.Error(w, "Author name cannot be empty", http.StatusBadRequest)
			return
		}

		query = query.Where("LOWER(title) LIKE ?", "%"+strings.ToLower(trimmedTitle)+"%") // Use LIKE for partial matching
	}

	// Check for spaces in the publisher name
	if publisher != "" {
		trimmedPublisher := strings.TrimSpace(publisher)
		publisherParts := strings.Fields(trimmedPublisher)

		if len(publisherParts) == 0 {
			http.Error(w, "Author name cannot be empty", http.StatusBadRequest)
			return
		}

		query = query.Where("LOWER(publisher) LIKE ?", "%"+strings.ToLower(trimmedPublisher)+"%") // Use LIKE for partial matching
	}

	// Handle search filtering across both fields
	if search != "" {
		trimmedSearch := strings.TrimSpace(search)
		query = query.Where("LOWER(author) LIKE ? OR LOWER(title) LIKE ? OR LOWER(publisher) LIKE ?", "%"+strings.ToLower(trimmedSearch)+"%", "%"+strings.ToLower(trimmedSearch)+"%" , "%"+strings.ToLower(trimmedSearch)+"%")
	}

	// Pagination
	limitStr := req.URL.Query().Get("limit")
	offsetStr := req.URL.Query().Get("offset")

	// Set default values for limit and offset if not provided
	if limitStr == "" {
		limitStr = "10" // Default limit
	}
	if offsetStr == "" {
		offsetStr = "0" // Default offset
	}

	// Convert limit and offset to int
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		http.Error(w, "Invalid limit value", http.StatusBadRequest)
		return
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		http.Error(w, "Invalid offset value", http.StatusBadRequest)
		return
	}

	// Apply pagination
	query = query.Limit(limit).Offset(offset)

	// Fetch books
	if err := query.Find(&bookModels).Error; err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Unable to fetch books", "data": nil})
		return
	}

	TotalCount := query.RowsAffected // returns found records count
	// Calculate current page and total pages
	currentPage := (offset / limit) + 1
	totalPages := int(math.Ceil(float64(TotalCount) / float64(limit)))

	// Sort and respond
	SortByID(bookModels)
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Books fetched successfully", "data": bookModels, "Total_count": TotalCount, "current_page": currentPage, "total_pages": totalPages})
}

func (r *Repository) GetBook(w http.ResponseWriter, req *http.Request) {
	bookModel := &models.Book{}
	vars := mux.Vars(req)
	id, exists := vars["id"]
	if !exists || id == "" {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
	}
	err := r.DB.Where("id = ?", id).First(bookModel, id).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Book not found", "data": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Book fetched successfully", "data": bookModel})
}

func (r *Repository) CreateBook(w http.ResponseWriter, req *http.Request) {
	book := &models.Book{}
    query := r.DB
	// Extract user from JWT
	user, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Check if the user is an admin
	if !user.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can create books", http.StatusForbidden)
		return
	}

	// Parse the incoming multipart form data (including file upload)
	err = req.ParseMultipartForm(MaxFileSize)
	if err != nil {
		http.Error(w, "File size exceeds limit of 10MB", http.StatusBadRequest)
		return
	}

	// Get the book data from form

	if err := schema.NewDecoder().Decode(book, req.Form); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	book.UserID = user.ID // Ensure the book is associated with the authenticated user (admin)


	// Handle file upload
	file, fileHeader, err := req.FormFile("image_path")
	if err != nil {
		http.Error(w, "Error while reading image file", http.StatusBadRequest)
		return
	}

	defer file.Close()

	// Validate file extension
	fileExtension := strings.ToLower(filepath.Ext(fileHeader.Filename))
	validExtensions := []string{".jpg", ".jpeg", ".png"}
	isValidExtension := false
	for _, ext := range validExtensions {
		if fileExtension == ext {
			isValidExtension = true
			break
		}
	}

	if !isValidExtension {
		http.Error(w, "Invalid file extension. Only jpg, jpeg, and png files are allowed", http.StatusBadRequest)
		return
	}

	// Validate file size
	fileSize := fileHeader.Size
	if fileSize > MaxFileSize {
		http.Error(w, "File size exceeds the 10 MB limit", http.StatusBadRequest)
		return
	}

	// Set local path for the image (inside the "uploads" directory)
	uploadDir := "uploads/" // Local directory for storing images
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		// If the directory doesn't exist, create it
		err := os.MkdirAll(uploadDir, os.ModePerm)
		if err != nil {
			http.Error(w, "Failed to create upload directory", http.StatusBadRequest)
			return
		}
	}
	currentTimestamp := time.Now().Unix() // Unix timestamp

	// Convert timestamp to string
	currentTimestampStr := fmt.Sprintf("%d", currentTimestamp)
	// Generate the image file path
	imagePath := uploadDir + "book_image_" + currentTimestampStr + fileExtension

	// Save the file to the local directory
	out, err := os.Create(imagePath)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusBadRequest)
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusBadRequest)
		return
	}

	// Store the image path in the book record
	book.ImagePath = &imagePath

	// Save book to the database
	if err := query.Create(book).Error; err != nil {
		http.Error(w, "Unable to create book", http.StatusBadRequest)
		return
	}

	// Respond with success
	writeJSON(w, http.StatusCreated, map[string]interface{}{"message": "Book created successfully", "data": book})
}

func (r *Repository) UpdateBook(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	bookModel := &models.Book{}
	vars := mux.Vars(req)

	// Extract ID from the URL parameters
	id, idExists := vars["id"]

	// Check if ID is not empty
	if !idExists || id == "" {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}

	// Check if the book exists in the database
	err := query.Where("id = ?", id).First(bookModel).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Book not found", "data": nil})
		return
	}

	// Parse the incoming multipart form data
	err = req.ParseMultipartForm(MaxFileSize)
	if err != nil {
		http.Error(w, "Unable to parse form data", http.StatusBadRequest)
		return
	}

	// Update bookModel fields from form data
	if title := req.FormValue("title"); title == "" {
		http.Error(w, "Title cannot be empty", http.StatusBadRequest)
		return
	} else {
		bookModel.Title = &title
	}

	if author := req.FormValue("author"); author != "" || len(author) == 0 {
		bookModel.Author = &author
	}

	if publisher := req.FormValue("publisher"); publisher != "" || len(publisher) == 0 {
		bookModel.Publisher = &publisher
	}

	// Handle file upload if provided
	if file, fileHeader, err := req.FormFile("image_path"); err == nil {
		defer file.Close()

		// Validate file extension
		fileExtension := strings.ToLower(filepath.Ext(fileHeader.Filename))
		validExtensions := []string{".jpg", ".jpeg", ".png"}
		isValidExtension := false
		for _, ext := range validExtensions {
			if fileExtension == ext {
				isValidExtension = true
				break
			}
		}
		if !isValidExtension {
			http.Error(w, "Invalid file extension. Only jpg, jpeg, and png files are allowed", http.StatusBadRequest)
			return
		}

		// Validate file size
		fileSize := fileHeader.Size
		if fileSize > MaxFileSize {
			http.Error(w, "File size exceeds the 10 MB limit", http.StatusBadRequest)
			return
		}

		// Set local path for the image
		uploadDir := "uploads/"
		currentTimestamp := time.Now().Unix() // Unix timestamp
		imagePath := fmt.Sprintf("%sbook_image_%d%s", uploadDir, currentTimestamp, fileExtension)

		// Save the file to the local directory
		out, err := os.Create(imagePath)
		if err != nil {
			http.Error(w, "Error while saving image", http.StatusBadRequest)
			return
		}

		defer out.Close()

		_, err = io.Copy(out, file)
		if err != nil {
			http.Error(w, "Error while saving image", http.StatusBadRequest)
			return
		}

		// Update image path in bookModel
		bookModel.ImagePath = &imagePath
	}

	// Update the book record
	err = query.Save(bookModel).Error
	if err != nil {
		http.Error(w, "Unable to update book", http.StatusInternalServerError)
		return
	}

	// Return a success message
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Book updated successfully", "data": bookModel})
}

func (r *Repository) DeleteBook(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	bookModel := &models.Book{}
	vars := mux.Vars(req)
	id, exists := vars["id"]

	// Check if ID is present in request
	if !exists || id == "" {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}

	// Check if book exists in DB
	err := query.Where("id = ?", id).First(bookModel).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Book not found", "data": nil})
		return
	}

	// Delete book and check if deletion was successful
	if query.Delete(bookModel).RowsAffected == 0 {
		http.Error(w, "Unable to delete book", http.StatusBadRequest)
		return
	}

	// Success response
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Book deleted successfully", "data": nil})
}

func (r *Repository) DownloadImage(w http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	id, exists := vars["id"]

	// Check if ID is present in the request
	if !exists || id == "" {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}

	// Retrieve the book from the database to get the image path
	bookModel := &models.Book{}
	err := r.DB.Where("id = ?", id).First(bookModel).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Book not found", "data": nil})
		return
	}

	// Check if ImagePath is set
	if bookModel.ImagePath == nil || *bookModel.ImagePath == "" {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Image not found", "data": nil})
		return
	}

	// Set the header to indicate that we're serving a file
	w.Header().Set("Content-Type", "image/jpeg") // Change if necessary based on your image type
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(*bookModel.ImagePath)))

	// Serve the image file
	http.ServeFile(w, req, *bookModel.ImagePath)
}

func (r *Repository) CreateUser(w http.ResponseWriter, req *http.Request) {
	// Extract JWT from request
	query := r.DB
	user, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Check if the user is an admin
	if !user.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can create users", http.StatusForbidden)
		return
	}

	userModel := &models.User{}

	// Parse the incoming multipart form data (including file upload)
	err = req.ParseMultipartForm(MaxFileSize)
	if err != nil {
		http.Error(w, "File size exceeds limit of 10MB", http.StatusBadRequest)
		return
	}

	// Get the user data from form

	if err := schema.NewDecoder().Decode(userModel, req.Form); err != nil {
		fmt.Println("err =>", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}


	// Handle file upload
	file, fileHeader, err := req.FormFile("image_path")
	if err != nil {
		http.Error(w, "Error while reading image file", http.StatusBadRequest)
		return
	}

	defer file.Close()

	// Validate file extension
	fileExtension := strings.ToLower(filepath.Ext(fileHeader.Filename))
	validExtensions := []string{".jpg", ".jpeg", ".png"}
	isValidExtension := false
	for _, ext := range validExtensions {
		if fileExtension == ext {
			isValidExtension = true
			break
		}
	}

	if !isValidExtension {
		http.Error(w, "Invalid file extension. Only jpg, jpeg, and png files are allowed", http.StatusBadRequest)
		return
	}

	// Validate file size
	fileSize := fileHeader.Size
	if fileSize > MaxFileSize {
		http.Error(w, "File size exceeds the 10 MB limit", http.StatusBadRequest)
		return
	}

	// Set local path for the image
	uploadDir := "uploads/" // Local directory for storing images
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		// If the directory doesn't exist, create it
		err := os.MkdirAll(uploadDir, os.ModePerm)
		if err != nil {
			http.Error(w, "Failed to create upload directory", http.StatusBadRequest)
			return
		}
	}
	currentTimestamp := time.Now().Unix() // Unix timestamp

	// Convert timestamp to string
	currentTimestampStr := fmt.Sprintf("%d", currentTimestamp)
	// Generate the image file path
	imagePath := uploadDir + "user_image_" + currentTimestampStr + fileExtension

	// Save the file to the local directory
	out, err := os.Create(imagePath)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusBadRequest)
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusBadRequest)
		return
	}

	// Store the image path in the user record
	userModel.ImagePath = &imagePath

	// Hash the password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(userModel.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing password", http.StatusBadRequest)
		return
	}

	// Set the hashed password back to the user model
	userModel.Password = string(hashedPassword) // No need for pointer here

	// Create the user in the database
	if err := query.Create(&userModel).Error; err != nil {
		http.Error(w, "Unable to create user", http.StatusBadRequest)
		return
	}

	// Assign a default role (e.g., "admin") to the new user
	userRole := models.UserRole{UserID: userModel.ID, RoleID: "user"} // Adjust this line
	if err := query.Create(&userRole).Error; err != nil {
		http.Error(w, "Unable to assign role to user", http.StatusBadRequest)
		return
	}

	// Get the user roles to return them in the response
	if err := query.Preload("UserRoles.Role").Where("id = ?", userModel.ID).First(userModel).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	// Return successful response
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"data":       userModel,
		"message":    "User created successfully",
	})
}

func (r *Repository) CreateMember(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	// Extract JWT from request
	user, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Check if the user is an admin
	if !user.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can create users", http.StatusForbidden)
		return
	}

	// Parse the incoming multipart form data (including file upload)
	err = req.ParseMultipartForm(25 << 20) // 25 MB
	if err != nil {
		http.Error(w, "File size exceeds limit of 25MB", http.StatusBadRequest)
		return
	}

	// Get the uploaded file
	file, fileHeader, err := req.FormFile("file")
	if err != nil {
		http.Error(w, "Error while reading file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file type
	fileExtension := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if fileExtension != ".csv" && fileExtension != ".xlsx" {
		http.Error(w, "File should be CSV or XLSX", http.StatusBadRequest)
		return
	}

	// Validate file size
	if fileHeader.Size > 25<<20 { // 25 MB
		http.Error(w, "File size exceeds the 25 MB limit", http.StatusBadRequest)
		return
	}

	// Parse the file based on its type
	var users []models.User
	var emailsInFile []string
	if fileExtension == ".csv" {
		users, err = parseCSV(file)
	} else if fileExtension == ".xlsx" {
		users, err = parseXLSX(file)
	}
	if err != nil {
		http.Error(w, "Error parsing file: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Collect emails from the uploaded users
	for _, u := range users {
		emailsInFile = append(emailsInFile, u.Email)
	}

	// Handle user creation logic and deletion of existing non-admin users
	for _, user := range users {
		// Check if the email already exists in the database
		var existingUser models.User
		result := query.Where("email = ?", user.Email).First(&existingUser)

		if result.Error == nil {
			// Email exists, ignore the record
			continue
		}

		// If user does not exist, create a new user
		if result.Error == gorm.ErrRecordNotFound {
			// Hash the password
			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(user.Password), bcrypt.DefaultCost)
			if err != nil {
				http.Error(w, "Error hashing password", http.StatusBadRequest)
				return
			}
			user.Password = string(hashedPassword)

			// Create the user in the database
			if err := query.Create(&user).Error; err != nil {
				http.Error(w, "Unable to create user: "+err.Error(), http.StatusBadRequest)
				return
			}

			// Assign a default role (e.g., "user") to the new user
			userRole := models.UserRole{UserID: user.ID, RoleID: "user"}
			if err := query.Create(&userRole).Error; err != nil {
				http.Error(w, "Unable to assign role to user: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
	}

	// Delete users not included in the uploaded file, excluding admins
	var existingUsers []models.User
	if err := query.Find(&existingUsers).Error; err != nil {
		http.Error(w, "Error retrieving existing users: "+err.Error(), http.StatusNotFound)
		return
	}

	for _, existingUser := range existingUsers {
		if !contains(emailsInFile, existingUser.Email) {
			// Check if the user is an admin
			var userRole models.UserRole
			if err := query.Where("user_id = ?", existingUser.ID).First(&userRole).Error; err != nil {
				// If the user is not found, proceed to delete
				continue
			}
			if userRole.RoleID != "admin" { // Only delete if not an admin
				// Delete associated user roles first
				if err := query.Where("user_id = ?", existingUser.ID).Delete(&models.UserRole{}).Error; err != nil {
					http.Error(w, "Error deleting user roles: "+err.Error(), http.StatusForbidden)
					return
				}
				// Delete the user
				if err := query.Delete(&existingUser).Error; err != nil {
					http.Error(w, "Error deleting user: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
	}

	// Return successful response
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "Members created successfully",
	})
}

func (r *Repository) UpdateUser(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	// Extract the target user ID from URL parameters
	vars := mux.Vars(req)
	targetID, ok := vars["id"]
	if !ok || targetID == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	// Retrieve the target user's record from the database
	userModel := &models.User{}
	if err := query.Where("id = ?", targetID).First(userModel).Error; err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
		return
	}

	// Extract the currently logged-in user from the JWT (to check admin privileges)
	currentUser, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if !currentUser.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can update users", http.StatusForbidden)
		return
	}

	// Parse the incoming multipart form data (including file upload)
	if err := req.ParseMultipartForm(MaxFileSize); err != nil {
		http.Error(w, "File size exceeds limit of 10MB", http.StatusBadRequest)
		return
	}

	// Decode form data into userModel.
	// (Ensure your models.User struct has proper `schema` tags for the fields you want to update.)
	if err := schema.NewDecoder().Decode(userModel, req.Form); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	// Handle file upload if a file is provided.
	file, fileHeader, err := req.FormFile("image_path")
	if err == nil {
		defer file.Close()

		fileExtension := strings.ToLower(filepath.Ext(fileHeader.Filename))
		validExtensions := []string{".jpg", ".jpeg", ".png"}
		isValidExtension := false
		for _, ext := range validExtensions {
			if fileExtension == ext {
				isValidExtension = true
				break
			}
		}
		if !isValidExtension {
			http.Error(w, "Invalid file extension. Only jpg, jpeg, and png files are allowed", http.StatusBadRequest)
			return
		}
		if fileHeader.Size > MaxFileSize {
			http.Error(w, "File size exceeds the 10 MB limit", http.StatusBadRequest)
			return
		}

		uploadDir := "uploads/"
		if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
			if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
				http.Error(w, "Failed to create upload directory", http.StatusBadRequest)
				return
			}
		}
		currentTimestamp := time.Now().Unix()
		imagePath := fmt.Sprintf("%suser_image_%d%s", uploadDir, currentTimestamp, fileExtension)

		out, err := os.Create(imagePath)
		if err != nil {
			http.Error(w, "Error while saving image", http.StatusBadRequest)
			return
		}
		defer out.Close()
		if _, err = io.Copy(out, file); err != nil {
			http.Error(w, "Error while saving image", http.StatusBadRequest)
			return
		}
		// Update the image path in the user record
		userModel.ImagePath = &imagePath
	}

	// If a new password is provided in the form, update it.
	// (Assume the form field "password" holds the new password.)
	if newPass := req.FormValue("password"); newPass != "" {
		hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "Error hashing password", http.StatusInternalServerError)
			return
		}
		userModel.Password = string(hashedPassword)
	}

	// Save the updated user record
	if err := query.Save(userModel).Error; err != nil {
		http.Error(w, "Unable to update user", http.StatusInternalServerError)
		return
	}

	// Reload the user record with preloaded user roles and their associated Role data
	if err := query.Preload("UserRoles.Role").Where("id = ?", userModel.ID).First(userModel).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	// Return the updated user profile
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":    userModel,
		"message": "User updated successfully",
	})
}

func (r *Repository) UpdateUserRole(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	// Extract user ID from the request URL
	vars := mux.Vars(req)
	userID, userIDExists := vars["id"]
	if !userIDExists || userID == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	// Extract the authenticated user (to check admin privileges)
	currentUser, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Only allow admins to update user roles
	if !currentUser.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can update user roles", http.StatusForbidden)
		return
	}

	var UserRole models.UserRole
	if err := json.NewDecoder(req.Body).Decode(&UserRole); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Ensure RoleID is provided
	if UserRole.RoleID == "" {
		http.Error(w, "Role ID is required", http.StatusBadRequest)
		return
	}

	// Check if the role exists
	var role models.Role
	if err := query.Where("id = ?", UserRole.RoleID).First(&role).Error; err != nil {
		http.Error(w, "Role not found", http.StatusNotFound)
		return
	}

	// Check if the user exists
	var user models.User
	if err := query.Where("id = ?", userID).First(&user).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Update the user's role
	var userRole models.UserRole
	if err := query.Where("user_id = ?", user.ID).First(&userRole).Error; err != nil {
		// No existing role, create a new one
		userRole = models.UserRole{
			UserID: user.ID,
			RoleID: UserRole.RoleID,
		}
		if err := query.Create(&userRole).Error; err != nil {
			http.Error(w, "Failed to assign role to user", http.StatusBadRequest)
			return
		}
	} else {
		// Update the existing role assignment
		userRole.RoleID = UserRole.RoleID
		if err := query.Save(&userRole).Error; err != nil {
			http.Error(w, "Failed to update user role", http.StatusBadRequest)
			return
		}
	}

	// Return success response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "User role updated successfully",
		"user_id": user.ID,
		"role_id": UserRole.RoleID,
	})
}

func (r *Repository) GetUserProfile(w http.ResponseWriter, req *http.Request) {
	// Extract the logged-in user's ID from the request context.
	currentUserID, ok := req.Context().Value("user_id").(string)
	if !ok || currentUserID == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get the ID from the URL parameters.
	vars := mux.Vars(req)
	paramID, exists := vars["id"]
	if !exists || paramID == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	// Check if the requested profile belongs to the current user.
	if currentUserID != paramID {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Profile not found", "data": nil})
		return
	}

	// Retrieve the user from the database.
	userModel := &models.User{}
	err := r.DB.Preload("UserRoles.Role").Where("id = ?", paramID).First(userModel).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
		return
	}

	// Return the user profile along with their roles.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       userModel,
		"message":    "User retrieved successfully",
	})
}

func (r *Repository) GetUsers(w http.ResponseWriter, req *http.Request) {
	var userModels []models.User
	query := r.DB

	// Ensure admins are not listed
	query = query.Joins("JOIN user_roles ON users.id = user_roles.user_id").
		Joins("JOIN roles ON user_roles.role_id = roles.id").
		Where("users.id NOT IN (SELECT user_id FROM user_roles WHERE role_id = (SELECT id FROM roles WHERE id = 'admin')) AND users.id IN (SELECT user_id FROM user_roles WHERE role_id = (SELECT id FROM roles WHERE id = 'user'))")

	// Filter by email
	email := req.URL.Query().Get("email")
	if email != "" {
		trimmedEmail := strings.TrimSpace(email)
		query = query.Where("LOWER(users.email) LIKE ?", "%"+strings.ToLower(trimmedEmail)+"%")
	}

	// Filter by name
	name := req.URL.Query().Get("name")
	if name != "" {
		trimmedName := strings.TrimSpace(name)
		query = query.Where("LOWER(users.name) LIKE ?", "%"+strings.ToLower(trimmedName)+"%")
	}

	// Search filter (applies to both name and email)
	search := req.URL.Query().Get("search")
	if search != "" {
		trimmedSearch := strings.TrimSpace(search)
		query = query.Where("LOWER(users.email) LIKE ? OR LOWER(users.name) LIKE ?", "%"+strings.ToLower(trimmedSearch)+"%", "%"+strings.ToLower(trimmedSearch)+"%")
	}

	// Pagination
	limitStr := req.URL.Query().Get("limit")
	offsetStr := req.URL.Query().Get("offset")

	// Set default values for limit and offset if not provided
	if limitStr == "" {
		limitStr = "10" // Default limit
	}
	if offsetStr == "" {
		offsetStr = "0" // Default offset
	}

	// Convert limit and offset to int
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		http.Error(w, "Invalid limit value", http.StatusBadRequest)
		return
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		http.Error(w, "Invalid offset value", http.StatusBadRequest)
		return
	}

	// Apply pagination
	query = query.Limit(limit).Offset(offset)
	// Ensure that we preload UserRoles and their associated Role
	if err := query.Preload("UserRoles.Role").Find(&userModels).Error; err != nil {
		http.Error(w, "Unable to retrieve users", http.StatusBadRequest)
		return
	}

	SortByID(userModels)
	TotalCount := query.RowsAffected // returns found records count
	// Calculate current page and total pages
	currentPage := (offset / limit) + 1
	totalPages := int(math.Ceil(float64(TotalCount) / float64(limit)))

	// Return successful response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       userModels,
		"total_count": TotalCount,
		"current_page": currentPage,
		"total_pages": totalPages,
		"message":    "Users retrieved successfully",
	})
}

func (r *Repository) GetAdmins(w http.ResponseWriter, req *http.Request) {
	var adminUsers []models.User
	query := r.DB

	// Extract the authenticated user
	currentUser, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Ensure only admins can access this endpoint
	if !currentUser.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can view admin users", http.StatusForbidden)
		return
	}

	// Query only admin users
	query = query.Joins("JOIN user_roles ON users.id = user_roles.user_id").
		Joins("JOIN roles ON user_roles.role_id = roles.id").
		Where("roles.id = ?", "admin")

	// Filter by email
	email := req.URL.Query().Get("email")
	if email != "" {
		trimmedEmail := strings.TrimSpace(email)
		query = query.Where("LOWER(users.email) LIKE ?", "%"+strings.ToLower(trimmedEmail)+"%")
	}

	// Filter by name
	name := req.URL.Query().Get("name")
	if name != "" {
		trimmedName := strings.TrimSpace(name)
		query = query.Where("LOWER(users.name) LIKE ?", "%"+strings.ToLower(trimmedName)+"%")
	}

	// Search filter (applies to both name and email)
	search := req.URL.Query().Get("search")
	if search != "" {
		trimmedSearch := strings.TrimSpace(search)
		query = query.Where("LOWER(users.email) LIKE ? OR LOWER(users.name) LIKE ?", "%"+strings.ToLower(trimmedSearch)+"%", "%"+strings.ToLower(trimmedSearch)+"%")
	}

	// Pagination
	limitStr := req.URL.Query().Get("limit")
	offsetStr := req.URL.Query().Get("offset")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 10 // Default limit
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0 // Default offset
	}

	// Apply pagination
	query = query.Limit(limit).Offset(offset)

	// Fetch admin users
	if err := query.Preload("UserRoles.Role").Find(&adminUsers).Error; err != nil {
		http.Error(w, "Unable to retrieve admins", http.StatusBadRequest)
		return
	}

	// Get total admin count
	var totalCount int64
	query.Model(&models.User{}).Count(&totalCount)

	// Calculate pagination details
	currentPage := (offset / limit) + 1
	totalPages := int(math.Ceil(float64(totalCount) / float64(limit)))
	SortByID(adminUsers)

	// Return response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":         adminUsers,
		"total_count":  totalCount,
		"current_page": currentPage,
		"total_pages":  totalPages,
		"message":      "Admins retrieved successfully",
	})
}

func (r *Repository) DeleteUser(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	// Extract user ID from URL parameters
	vars := mux.Vars(req)
	userID, userIDExists := vars["id"]
	if !userIDExists || userID == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
		return
	}

	// Extract the authenticated user (to check admin privileges)
	currentUser, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Only allow admins to delete users
	if !currentUser.IsAdmin() {
		http.Error(w, "Forbidden: Only admins can delete users", http.StatusForbidden)
		return
	}

	// Check if the user exists
	var user models.User
	if err := query.Where("id = ?", userID).Preload("UserRoles.Role").First(&user).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Check if the user to be deleted is an admin
	for _, userRole := range user.UserRoles {
		if userRole.RoleID == "admin" {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{"message": "Admin cannot be deleted!", "data": nil})
			return
		}
	}

	// Start transaction to ensure atomic operation
	tx := query.Begin()

	// Delete associated roles first to avoid foreign key constraint errors
	if err := tx.Where("user_id = ?", userID).Delete(&models.UserRole{}).Error; err != nil {
		tx.Rollback()
		http.Error(w, "Failed to delete user roles", http.StatusInternalServerError)
		return
	}

	// Delete the user
	if err := tx.Delete(&user).Error; err != nil {
		tx.Rollback()
		http.Error(w, "Failed to delete user", http.StatusInternalServerError)
		return
	}

	// Commit transaction
	tx.Commit()

	// Return success response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "User deleted successfully",
		"user_id": userID,
	})
}

func (r *Repository) Login(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	userModel := &models.User{}
	var credentials struct {
		Email       string `json:"email"`
		PhoneNumber string `json:"phone_number"`
	}

	// Decode the request body
	if err := json.NewDecoder(req.Body).Decode(&credentials); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Validate that at least one of Email or PhoneNumber is provided
	if credentials.Email == "" && credentials.PhoneNumber == "" {
		http.Error(w, "Either Email ID or Phone number is required", http.StatusBadRequest)
		return
	}

	// Find user in the database using either Email or PhoneNumber
	if credentials.Email != "" {
		err := query.Where("email = ?", credentials.Email).First(userModel).Error
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
			return
		}
	} else {
		err := query.Where("phone_number = ?", credentials.PhoneNumber).First(userModel).Error
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
			return
		}
	}

	// Update user fields
	now := time.Now().UTC()

	if userModel.Status == "non_active" {
			userModel.ActiveOn = &now
	}
	userModel.Status = "active"
	userModel.LastActive = &now

	// Check if user already has a token
	if userModel.Token == "" {
		// Generate JWT token
		token, err := generateJWT(strconv.FormatUint(uint64(userModel.ID), 10))
		if err != nil {
			http.Error(w, "Error generating token", http.StatusBadRequest)
			return
		}

		// Save the token in the user record
		userModel.Token = token
	}

	// Save updated user information first
	if err := query.Save(userModel).Error; err != nil {
		http.Error(w, "Error saving user record", http.StatusBadRequest)
		return
	}

	// Now retrieve the user along with its roles
	err := query.Preload("UserRoles.Role").Where("id = ?", userModel.ID).First(userModel).Error
	if err != nil {
		http.Error(w, "Error retrieving updated user record", http.StatusBadRequest)
		return
	}

	// Respond with token and user info
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       userModel,
		"message":    "Login successful",
	})
}

func (r *Repository) Logout(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	authHeader := req.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "Missing token", http.StatusUnauthorized)
		return
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		http.Error(w, "Invalid token format", http.StatusUnauthorized)
		return
	}

	tokenString := parts[1]

	// Retrieve user from the database using the token
	user := &models.User{}
	err := query.Where("token = ?", tokenString).First(user).Error
	if err != nil {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// Check if the token is expired
	claims := &jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_SECRET")), nil
	})

	if err != nil || !token.Valid {
		// Token is invalid; remove it from the database
		user.Token = ""
		query.Save(user)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"message": "Token is expired or invalid"})
		return
	}

	// Token is valid; remove it from the database
	user.Token = ""
	if err := query.Save(user).Error; err != nil {
		http.Error(w, "Error removing token from user record", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Logout successful",
	})
}

func (r *Repository) Register(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	userModel := &models.User{}

	// Hash the password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(userModel.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing password", http.StatusBadRequest)
		return
	}

	// Set the hashed password back to the user model
	userModel.Password = string(hashedPassword)

	// Set ActiveOn and LastActive to nil
	userModel.ActiveOn = nil
	userModel.LastActive = nil

	// Parse the incoming multipart form data (including file upload)
	err = req.ParseMultipartForm(MaxFileSize)
	if err != nil {
		http.Error(w, "File size exceeds limit of 10MB", http.StatusBadRequest)
		return
	}

	// Get the user data from form

	if err := schema.NewDecoder().Decode(userModel, req.Form); err != nil {
		fmt.Println("err =>", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}


	// Handle file upload
	file, fileHeader, err := req.FormFile("image_path")
	if err != nil {
		http.Error(w, "Error while reading image file", http.StatusBadRequest)
		return
	}

	defer file.Close()

	// Validate file extension
	fileExtension := strings.ToLower(filepath.Ext(fileHeader.Filename))
	validExtensions := []string{".jpg", ".jpeg", ".png"}
	isValidExtension := false
	for _, ext := range validExtensions {
		if fileExtension == ext {
			isValidExtension = true
			break
		}
	}

	if !isValidExtension {
		http.Error(w, "Invalid file extension. Only jpg, jpeg, and png files are allowed", http.StatusBadRequest)
		return
	}

	// Validate file size
	fileSize := fileHeader.Size
	if fileSize > MaxFileSize {
		http.Error(w, "File size exceeds the 10 MB limit", http.StatusBadRequest)
		return
	}

	// Set local path for the image
	uploadDir := "uploads/" // Local directory for storing images
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		// If the directory doesn't exist, create it
		err := os.MkdirAll(uploadDir, os.ModePerm)
		if err != nil {
			http.Error(w, "Failed to create upload directory", http.StatusBadRequest)
			return
		}
	}
	currentTimestamp := time.Now().Unix() // Unix timestamp

	// Convert timestamp to string
	currentTimestampStr := fmt.Sprintf("%d", currentTimestamp)
	// Generate the image file path
	imagePath := uploadDir + "user_image_" + currentTimestampStr + fileExtension

	// Save the file to the local directory
	out, err := os.Create(imagePath)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusBadRequest)
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusBadRequest)
		return
	}

	// Store the image path in the book record
	userModel.ImagePath = &imagePath


	// Create the user in the database
	if err := query.Create(&userModel).Error; err != nil {
		http.Error(w, "Email or phone number is already taken. Please chose a different one.", http.StatusBadRequest)
		return
	}

	// Assign a default role (e.g., "admin") to the new user
	userRole := models.UserRole{UserID: userModel.ID, RoleID: "user"} // Adjust this line
	if err := query.Create(&userRole).Error; err != nil {
		http.Error(w, "Unable to assign role to user", http.StatusBadRequest)
		return
	}

	// Get the user roles to return them in the response
	if err := query.Preload("UserRoles.Role").Where("id = ?", userModel.ID).First(userModel).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	if err := query.Save(userModel).Error; err != nil {
		http.Error(w, "Error saving token to user record", http.StatusBadRequest)
		return
	}

	// Return successful response with user data
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"data":    userModel,
		"message": "User registered successfully",
	})
}

func (r *Repository) ForgetPassword(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	user := &models.User{}
	var request struct {
		Email string `json:"email"`
		PhoneNumber string `json:"phone_number"`
	}
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Validate that at least one of Email or PhoneNumber is provided
	if request.Email == "" && request.PhoneNumber == "" {
		http.Error(w, "Either Email ID or Phone number is required", http.StatusBadRequest)
		return
	}

	if err := query.Where("email = ?", request.Email).First(user).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Generate a unique reset token
	resetToken := make([]byte, 32) // 32 bytes = 256 bits
	if _, err := rand.Read(resetToken); err != nil {
		http.Error(w, "Error generating reset token", http.StatusBadRequest)
		return
	}
	tokenString := hex.EncodeToString(resetToken)

	// Save the token in the database (you might want to create a separate table for reset tokens)
	user.ResetToken = tokenString // Assuming you have added ResetToken field in the User model
	if err := query.Save(user).Error; err != nil {
		http.Error(w, "Error saving reset token", http.StatusBadRequest)
		return
	}

	// Send reset password email
	resetLink := fmt.Sprintf("http://localhost:8000/api/reset_password?token=%s", tokenString)
	if err := sendResetEmail(user.Name, request.Email, resetLink); err != nil {
		http.Error(w, "Error sending email", http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Reset password link sent to" + " " + user.Name})
}

func (r *Repository) ResetPassword(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	var request struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}

	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Find user by reset token
	user := &models.User{}
	if err := query.Where("reset_token = ?", request.Token).First(user).Error; err != nil {
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	// Hash the new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(request.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing password", http.StatusInternalServerError)
		return
	}

	// Update the user's password
	user.Password = string(hashedPassword)
	user.ResetToken = ""

	if err := query.Save(user).Error; err != nil {
		http.Error(w, "Error updating password", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Password has been reset."})
}

func (r *Repository) ChangePassword(w http.ResponseWriter, req *http.Request) {
	query := r.DB
	user, err := extractUserFromJWT(req, query)
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var request struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}

	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Validate old password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(request.OldPassword)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"message": "Invalid old password"})
		return
	}

	// Hash the new password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(request.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing new password", http.StatusBadRequest)
		return
	}

	// Update the user's password
	user.Password = string(hashedPassword)
	if err := query.Save(user).Error; err != nil {
		http.Error(w, "Error updating password", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Password changed successfully"})
}

func (r *Repository) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		query := r.DB
		authHeader := req.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Missing token", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, "Invalid token format", http.StatusUnauthorized)
			return
		}

		tokenString := parts[1]

		// Fetch user from the database using the token
		user := &models.User{}
		err := query.Where("token = ?", tokenString).First(user).Error
		if err != nil {
			http.Error(w, "You need to login before accessing it", http.StatusBadRequest)
			return
		}

		// Check if the user is an admin.
		var userRole models.UserRole
		if err := query.Where("user_id = ? AND role_id = ?", user.ID, "admin").First(&userRole).Error; err != nil {
			http.Error(w, "Only admin can access this.", http.StatusUnauthorized)
			return
		}

		// Check if the token is expired
		claims := &jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(os.Getenv("JWT_SECRET")), nil
		})

		if err != nil || !token.Valid {
			// Token is invalid; remove it from the database
			user.Token = ""
			query.Save(user)
			http.Error(w, "Token is expired or invalid", http.StatusUnauthorized)
			return
		}

		// Extract user ID from claims
		userID, ok := (*claims)["user_id"].(string)
		if !ok {
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		// Store the user ID in the context
		ctx := context.WithValue(req.Context(), "user_id", userID)
		next.ServeHTTP(w, req.WithContext(ctx))
	})
}

func extractUserFromJWT(req *http.Request, db *gorm.DB) (*models.User, error) {
	tokenString := req.Header.Get("Authorization")
	if tokenString == "" {
		return nil, fmt.Errorf("missing token")
	}
	// Ensure "Bearer " prefix is removed
	tokenString = strings.TrimPrefix(tokenString, "Bearer ")

	// Parse the token
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_SECRET")), nil // Ensure you are using the correct secret key
	})
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Extract claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}


	// Extract user_id
	userID, ok := claims["user_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid user_id claim")
	}

	// Fetch user from DB
	var user models.User
	err = db.Preload("UserRoles").Where("id = ?", userID).First(&user).Error
	if err != nil {
		return nil, fmt.Errorf("user not found")
	}
	return &user, nil
}

// sendResetEmail sends the reset password email
func sendResetEmail(name, to, resetLink string) error {
	from := mail.NewEmail(name, "barkahw32@gmail.com")
	subject := "Password Reset Request"
	toEmail := mail.NewEmail(name, to)
	plainTextContent := fmt.Sprintf("Hello %s,\n\nClick the link to reset your password: %s\n\nThank you!", name, resetLink)
	htmlContent := fmt.Sprintf("<strong>Hello %s,</strong><br><br>Click the link to reset your password: <a href='%s'>Reset Password</a><br><br>Thank you!", name, resetLink)
	message := mail.NewSingleEmail(from, subject, toEmail, plainTextContent, htmlContent)

	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		log.Println("Error sending email:", err)
		return err
	}

	log.Println("Email sent with status code:", response.StatusCode)
	return nil
}

func MigrateAll(db *gorm.DB) error {
	if err := models.MigrateUser(db); err != nil {
		return err
	}
	if err := models.MigrateRole(db); err != nil {
		return err
	}
	if err := models.MigrateUserRole(db); err != nil {
		return err
	}
	if err := models.MigrateBook(db); err != nil {
		return err
	}
	return nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func parseCSV(file io.Reader) ([]models.User, error) {
	reader := csv.NewReader(file)
	users := []models.User{}

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("error reading header: %v", err)
	}

	expectedHeader := []string{"name", "email", "phone_number", "status", "active_on"}
	// Validate header columns
	if len(header) != len(expectedHeader) && len(header) != 4 {
		return nil, fmt.Errorf("invalid XLSX format: expected %d columns or 4 columns, got %d", len(expectedHeader), len(header))
	}

	for i, col := range header {
		if col != expectedHeader[i] {
			return nil, fmt.Errorf("invalid CSV format: expected column %s, got %s", expectedHeader[i], col)
		}
	}

	for rowIndex := 1; ; rowIndex++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("error reading row %d: %v", rowIndex+1, err)
		}

		// Ensure the record has at least 4 relevant fields
		if len(record) < 4 {
			return nil, fmt.Errorf("invalid CSV format at row %d: expected at least 4 columns, got %d", rowIndex+1, len(record))
		}

		user := models.User{
			Name:        strings.TrimSpace(record[0]),
			Email:       strings.TrimSpace(record[1]),
			PhoneNumber: strings.TrimSpace(record[2]),
			Status:      strings.TrimSpace(record[3]),
		}

		// Parse ActiveOn field if provided
		if len(record) > 4 {
			activeOnStr := strings.TrimSpace(record[4])
			if activeOnStr != "" { // Only attempt to parse if the string is not empty
				activeOn, err := time.Parse("2006-01-02", activeOnStr)
				if err != nil {
					fmt.Println("Error parsing date:", err)
					continue // Skip this entry but continue processing
				}
				user.ActiveOn = &activeOn
			}
		}

		// Validate required fields
		if user.Name == "" || user.Email == "" || user.PhoneNumber == "" {
			return nil, fmt.Errorf("invalid data at row %d: name, email and phone number are required fields", rowIndex+1)
		}

		users = append(users, user)
	}

	return users, nil
}

func parseXLSX(file io.Reader) ([]models.User, error) {
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %v", err)
	}

	xlFile, err := xlsx.OpenBinary(data)
	if err != nil {
		return nil, fmt.Errorf("error opening XLSX file: %v", err)
	}

	users := []models.User{}
	sheet := xlFile.Sheets[0]

	expectedHeader := []string{"name", "email", "phone_number", "status", "active_on"}
	// Validate header columns
	if len(sheet.Rows[0].Cells) != len(expectedHeader) && len(sheet.Rows[0].Cells) != 4 {
		return nil, fmt.Errorf("invalid XLSX format: expected %d columns or 4 columns, got %d", len(expectedHeader), len(sheet.Rows[0].Cells))
	}

	for i, cell := range sheet.Rows[0].Cells {
		if cell.String() != expectedHeader[i] {
			return nil, fmt.Errorf("invalid XLSX format: expected column %s, got %s", expectedHeader[i], cell.String())
		}
	}

	for rowIndex, row := range sheet.Rows[1:] {
		// Ensure the row has at least 4 relevant columns
		if len(row.Cells) < 4 {
			return nil, fmt.Errorf("invalid XLSX format at row %d: expected at least 4 columns, got %d", rowIndex+2, len(row.Cells))
		}

		user := models.User{
			Name:        strings.TrimSpace(row.Cells[0].String()),
			Email:       strings.TrimSpace(row.Cells[1].String()),
			PhoneNumber: strings.TrimSpace(row.Cells[2].String()),
			Status:      strings.TrimSpace(row.Cells[3].String()),
		}

		// Parse ActiveOn field if provided
		if len(row.Cells) > 4 {
			activeOnStr := strings.TrimSpace(row.Cells[4].String())
			if activeOnStr != "" { // Only attempt to parse if the string is not empty
				activeOn, err := time.Parse("2006-01-02", activeOnStr)
				if err != nil {
					fmt.Println("Error parsing date:", err)
					continue // Skip this entry but continue processing
				}
				user.ActiveOn = &activeOn
			}
		}

		// Validate required fields
		if user.Name == "" || user.Email == "" || user.PhoneNumber == "" || user.Status == "" {
			return nil, fmt.Errorf("invalid data at row %d: name, email, phone number, and status are required fields", rowIndex+2)
		}

		users = append(users, user)
	}

	return users, nil
}

func (r *Repository) SetupRoutes(rts *mux.Router) {
	// Public routes
	rts.HandleFunc("/register", r.Register).Methods("POST")
	rts.HandleFunc("/login", r.Login).Methods("POST")

	// Protected routes
	protected := rts.PathPrefix("/api").Subrouter()
	protected.Use(r.AuthMiddleware)
	protected.HandleFunc("/create_books", r.CreateBook).Methods("POST")
	protected.HandleFunc("/delete_books/{id}", r.DeleteBook).Methods("DELETE")
	protected.HandleFunc("/update_books/{id}", r.UpdateBook).Methods("PUT")
	protected.HandleFunc("/books", r.GetBooks).Methods("GET")
	protected.HandleFunc("/get_books/{id}", r.GetBook).Methods("GET")
	protected.HandleFunc("/download_image/{id}", r.DownloadImage).Methods("GET")
	protected.HandleFunc("/logout", r.Logout).Methods("POST")
	protected.HandleFunc("/profile/{id}", r.GetUserProfile).Methods("GET")
	protected.HandleFunc("/create_user", r.CreateUser).Methods("POST")
	protected.HandleFunc("/create_member", r.CreateMember).Methods("POST")
	protected.HandleFunc("/update_user/{id}", r.UpdateUser).Methods("PUT")
	protected.HandleFunc("/update_user_role/{id}", r.UpdateUserRole).Methods("PUT")
	protected.HandleFunc("/users", r.GetUsers).Methods("GET")
	protected.HandleFunc("/admin_users", r.GetAdmins).Methods("GET")
	protected.HandleFunc("/users/{id}", r.DeleteUser).Methods("DELETE")
	protected.HandleFunc("/reset_password", r.ResetPassword).Methods("POST")
	protected.HandleFunc("/change_password", r.ChangePassword).Methods("POST")
	protected.HandleFunc("/forget_password", r.ForgetPassword).Methods("POST")


}

func generateJWT(userID string) (string, error) {
	secretKey := os.Getenv("JWT_SECRET")

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID,
		"exp":     time.Now().Add(time.Hour * 24).Unix(), // Token expires in 24 hours
	})

	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		return "", err
	}
	return tokenString, nil
}


func main(){
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	config := &storage.Config{
		Host:     os.Getenv("DB_HOST"),
		Port:     os.Getenv("DB_PORT"),
		User:     os.Getenv("DB_USER"),
		Password: os.Getenv("DB_PASSWORD"),
		DBName:   os.Getenv("DB_NAME"),
		SSLMode:  os.Getenv("SSL_MODE"),
	}

	//hashedPassword, err := bcrypt.GenerateFromPassword([]byte("123456789"), bcrypt.DefaultCost)
	//if err != nil {
	//	fmt.Println("Error hashing password:", err)
	//	return
	//}
	//fmt.Println("Hashed Password:", string(hashedPassword))

	db, err := storage.NewConnection(config)
	if err != nil {
		log.Fatal("could not connect to database")
	}

	if err := MigrateAll(db); err != nil {
		log.Fatal("could not migrate database")
	}

	r := Repository{
		DB: db,
	}
	rts := mux.NewRouter()
	r.SetupRoutes(rts)

	fmt.Println("Server running on port 8000")
	log.Fatal(http.ListenAndServe(":8000", rts))
}