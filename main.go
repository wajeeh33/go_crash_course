package main


import (
	"context"
	"encoding/hex"
	"crypto/rand"
	"io"
	"log"
	"encoding/json"
    "github.com/golang-jwt/jwt/v5"
    "fmt"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/gorilla/schema"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"gorm.io/gorm"
	"github.com/wajeeh33/go_crash_course/storage"
	"github.com/wajeeh33/go_crash_course/models"
	"net/smtp"
	"os"
	"path/filepath"
	//"regexp"
	"strconv"
	"strings"
	"time"
	"sort"
)

type Repository struct {
	DB *gorm.DB
}

type HasID interface {
	GetID() uint
}

// Generic function to sort slices by ID
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

const MaxFileSize = 10 * 1024 * 1024 // 10 MB

func (r *Repository) GetBooks(w http.ResponseWriter, req *http.Request) {
	bookModels := []models.Book{}
	query := r.DB

	// Read query parameters
	author := req.URL.Query().Get("author")
	title := req.URL.Query().Get("title")
	publisher := req.URL.Query().Get("publisher")
	search := req.URL.Query().Get("search") // New search parameter

	// Check for spaces in the author name and handle filtering
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

	// Check for spaces in the title and handle filtering
	if title != "" {
		trimmedTitle := strings.TrimSpace(title)
		TitleParts := strings.Fields(trimmedTitle)

		if len(TitleParts) == 0 {
			http.Error(w, "Author name cannot be empty", http.StatusBadRequest)
			return
		}

		query = query.Where("LOWER(title) LIKE ?", "%"+strings.ToLower(trimmedTitle)+"%") // Use LIKE for partial matching
	}

	// Check for spaces in the publisher name and handle filtering
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

	// Sort and respond
	SortByID(bookModels)
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Books fetched successfully", "data": bookModels})
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
	// Extract user from JWT
	user, err := extractUserFromJWT(req, r.DB)
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
	book := &models.Book{}
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
	currentTimestamp := time.Now().Unix() // Unix timestamp (seconds since January 1, 1970)

	// Convert timestamp to string
	currentTimestampStr := fmt.Sprintf("%d", currentTimestamp)
	// Generate the image file path (you can modify this logic to handle naming conflicts)
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

	// Store the image path in the book record (assuming you have an `ImagePath` field in the `Book` model)
	book.ImagePath = &imagePath

	// Save book to the database
	if err := r.DB.Create(book).Error; err != nil {
		fmt.Println("error creating book:", err)
		http.Error(w, "Unable to create book", http.StatusBadRequest)
		return
	}

	// Respond with success
	writeJSON(w, http.StatusCreated, map[string]interface{}{"message": "Book created successfully", "data": book})
}

func (r *Repository) UpdateBook(w http.ResponseWriter, req *http.Request) {
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
	err := r.DB.Where("id = ?", id).First(bookModel).Error
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

		// Set local path for the image (inside the "uploads" directory)
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
	err = r.DB.Save(bookModel).Error
	if err != nil {
		http.Error(w, "Unable to update book", http.StatusInternalServerError)
		return
	}

	// Return a success message
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Book updated successfully", "data": bookModel})
}

func (r *Repository) DeleteBook(w http.ResponseWriter, req *http.Request) {
	bookModel := &models.Book{} // Initialize book model
	vars := mux.Vars(req)
	id, exists := vars["id"]

	// Check if ID is present in request
	if !exists || id == "" {
		http.Error(w, "Book ID is required", http.StatusBadRequest)
		return
	}

	// Check if book exists in DB
	err := r.DB.Where("id = ?", id).First(bookModel).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "Book not found", "data": nil})
		return
	}

	// Delete book and check if deletion was successful
	if r.DB.Delete(bookModel).RowsAffected == 0 {
		http.Error(w, "Unable to delete book", http.StatusBadRequest)
		return
	}

	// Success response
	writeJSON(w, http.StatusOK, map[string]string{"message": "Book deleted successfully"})
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
	user, err := extractUserFromJWT(req, r.DB)
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

	// Decode the request body into the User model
	if err := json.NewDecoder(req.Body).Decode(&userModel); err != nil {
		fmt.Println("error decoding request body:", err)
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Hash the password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(userModel.Password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "Error hashing password", http.StatusBadRequest)
		return
	}

	// Set the hashed password back to the user model
	userModel.Password = string(hashedPassword) // No need for pointer here

	// Create the user in the database
	if err := r.DB.Create(&userModel).Error; err != nil {
		http.Error(w, "Unable to create user", http.StatusBadRequest)
		return
	}

	// Assign a default role (e.g., "admin") to the new user
	userRole := models.UserRole{UserID: userModel.ID, RoleID: "user"} // Adjust this line
	if err := r.DB.Create(&userRole).Error; err != nil {
		http.Error(w, "Unable to assign role to user", http.StatusBadRequest)
		return
	}

	// Get the user roles to return them in the response
	var userRoles []models.UserRole
	if err := r.DB.Preload("Role").Where("user_id = ?", userModel.ID).Find(&userRoles).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	// Return successful response
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"data":       userModel,
		"user_roles": userRoles,
		"message":    "User created successfully",
	})
}

func (r *Repository) GetUserProfile(w http.ResponseWriter, req *http.Request) {
	userModel := &models.User{}
	vars := mux.Vars(req)
	id, exists := vars["id"]
	if !exists || id == "" {
		http.Error(w, "User ID is required", http.StatusBadRequest)
	}
	err := r.DB.Where("id = ?", id).First(userModel, id).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
		return
	}

	// Get the user roles to return them in the response
	 userRoles:= []models.UserRole{}
	if err := r.DB.Preload("Role").Where("user_id = ?", userModel.ID).Find(&userRoles).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	// Return successful response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       userModel,
		"user_roles": userRoles,
		"message":    "User retrieved successfully",
	})

}

func (r *Repository) GetUsers(w http.ResponseWriter, req *http.Request) {
	userModels := []models.User{}
	query := r.DB

	email := req.URL.Query().Get("email")
	name := req.URL.Query().Get("name")
	search := req.URL.Query().Get("search")

	// Check for spaces in the author name and handle filtering
	if email != "" {
		trimmedEmail := strings.TrimSpace(email)
		emailParts := strings.Fields(trimmedEmail)

		if len(emailParts) == 0 {
			http.Error(w, "email cannot be empty", http.StatusBadRequest)
			return
		}

		// Prepare the query for both first and last names
		query = query.Where("LOWER(email) LIKE ?", "%"+strings.ToLower(trimmedEmail)+"%")
	}

	// Check for spaces in the title and handle filtering
	if name != "" {
		trimmedName := strings.TrimSpace(name)
		NameParts := strings.Fields(trimmedName)

		if len(NameParts) == 0 {
			http.Error(w, "Author name cannot be empty", http.StatusBadRequest)
			return
		}

		query = query.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(trimmedName)+"%") // Use LIKE for partial matching
	}

	// Handle search filtering across both fields
	if search != "" {
		trimmedSearch := strings.TrimSpace(search)
		query = query.Where("LOWER(email) LIKE ? OR LOWER(name) LIKE ?", "%"+strings.ToLower(trimmedSearch)+"%", "%"+strings.ToLower(trimmedSearch)+"%")
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

	// Return successful response
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       userModels,
		"message":    "Users retrieved successfully",
	})

}

func (r *Repository) Login(w http.ResponseWriter, req *http.Request) {
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
		err := r.DB.Where("email = ?", credentials.Email).First(userModel).Error
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
			return
		}
	} else {
		err := r.DB.Where("phone_number = ?", credentials.PhoneNumber).First(userModel).Error
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
			return
		}
	}

	UserRoles := []models.UserRole{}
	if err := r.DB.Preload("Role").Where("user_id = ?", userModel.ID).Find(&UserRoles).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
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

	// Save updated user information
	if err := r.DB.Save(userModel).Error; err != nil {
		http.Error(w, "Error saving user record", http.StatusBadRequest)
		return
	}

	// Respond with token and user info
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":       userModel,
		"user_roles": UserRoles,
		"message":    "Login successful",
	})
}

func (r *Repository) Logout(w http.ResponseWriter, req *http.Request) {
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
	err := r.DB.Where("token = ?", tokenString).First(user).Error
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
		r.DB.Save(user)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"message": "Token is expired or invalid"})
		return
	}

	// Token is valid; remove it from the database
	user.Token = ""
	if err := r.DB.Save(user).Error; err != nil {
		http.Error(w, "Error removing token from user record", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Logout successful",
	})
}

func (r *Repository) Register(w http.ResponseWriter, req *http.Request) {
	userModel := &models.User{}

	// Decode the request body into the User model
	if err := json.NewDecoder(req.Body).Decode(&userModel); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

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

	// Create the user in the database
	if err := r.DB.Create(&userModel).Error; err != nil {
		http.Error(w, "Email is already taken. Please chose a different one.", http.StatusBadRequest)
		return
	}

	// Assign a default role (e.g., "admin") to the new user
	userRole := models.UserRole{UserID: userModel.ID, RoleID: "user"} // Adjust this line
	if err := r.DB.Create(&userRole).Error; err != nil {
		http.Error(w, "Unable to assign role to user", http.StatusBadRequest)
		return
	}

	// Get the user roles to return them in the response
	 userRoles := []models.UserRole{}
	if err := r.DB.Preload("Role").Where("user_id = ?", userModel.ID).Find(&userRoles).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	if err := r.DB.Save(userModel).Error; err != nil {
		http.Error(w, "Error saving token to user record", http.StatusBadRequest)
		return
	}

	// Return successful response with user data
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"data":    userModel,
        "Role": userRoles[0].Role,
		"message": "User registered successfully",
	})
}

func (r *Repository) ForgetPassword(w http.ResponseWriter, req *http.Request) {
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


	//// Validate email using the model's emailRegex function
	//if emailRegex(request.Email) {
	//
	//	http.Error(w, "Invalid email format", http.StatusBadRequest)
	//	return
	//}
	//// Validate email using the model's emailRegex function
	//if phoneNumberRegex(request.PhoneNumber) {
	//	http.Error(w, "Invalid phone number format", http.StatusBadRequest)
	//	return
	//}

	if err := r.DB.Where("email = ?", request.Email).First(user).Error; err != nil {
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
	if err := r.DB.Save(user).Error; err != nil {
		fmt.Println("error reset token ", err)
		http.Error(w, "Error saving reset token", http.StatusBadRequest)
		return
	}

	// Send reset password email
	resetLink := fmt.Sprintf("http://localhost:8000/reset_password?token=%s", tokenString)
	fmt.Println("resetLink ==>", resetLink)
	if err := sendResetEmail(user.Name, request.Email, resetLink); err != nil {
		fmt.Println("error => ", err)
		http.Error(w, "Error sending email", http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Reset link sent to email."})
}

func (r *Repository) ResetPassword(w http.ResponseWriter, req *http.Request) {
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
	if err := r.DB.Where("reset_token = ?", request.Token).First(user).Error; err != nil {
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

	if err := r.DB.Save(user).Error; err != nil {
		http.Error(w, "Error updating password", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Password has been reset."})
}

func (r *Repository) ChangePassword(w http.ResponseWriter, req *http.Request) {
	user, err := extractUserFromJWT(req, r.DB)
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
	if err := r.DB.Save(user).Error; err != nil {
		http.Error(w, "Error updating password", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "Password changed successfully"})
}

func (r *Repository) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
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
		err := r.DB.Where("token = ?", tokenString).First(user).Error
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
			r.DB.Save(user)
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
	from := "barkahw32@gmail.com"
	password := os.Getenv("EMAIL_PASSWORD")
	fmt.Println("EMAIL_PASSWORD:", password)
	hostEmail := "smtp.gmail.com"
	smtpPort := "587"

	// Set up authentication information.
	auth := smtp.PlainAuth("", from, password, hostEmail)

	// Message
	subject := "Password Reset Request"
	body := fmt.Sprintf("Hello %s,\n\nClick the link to reset your password: %s\n\nThank you!", name, resetLink)
	msg := []byte("To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"\r\n" +
		body + "\r\n")

	// Connect to the server, authenticate, and send the email
	err := smtp.SendMail(hostEmail+":"+smtpPort, auth, from, []string{to}, msg)
	return err
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

//// Email validation regex
//func emailRegex(email string) bool {
//	re := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
//	fmt.Println("regs => ", re)
//	return re.MatchString(email)
//}

// Phone number validation regex
//func phoneNumberRegex(phone string) bool {
//	re := regexp.MustCompile(`^\+\d{8,15}$`)
//	return re.MatchString(phone)
//}

func (r *Repository) SetupRoutes(rts *mux.Router) {
	// Public routes
	rts.HandleFunc("/register", r.Register).Methods("POST") // Registration endpoint
	rts.HandleFunc("/login", r.Login).Methods("POST")
	rts.HandleFunc("/create_user", r.CreateUser).Methods("POST")
	rts.HandleFunc("/forget_password", r.ForgetPassword).Methods("POST")

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
	protected.HandleFunc("/users", r.GetUsers).Methods("GET")
	protected.HandleFunc("/reset_password", r.ResetPassword).Methods("POST")
	protected.HandleFunc("/change_password", r.ChangePassword).Methods("POST")

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