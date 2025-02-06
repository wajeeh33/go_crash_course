package main


import (
	"context"
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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	fmt.Println("user", user)
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
	var userRoles []models.UserRole
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
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	// Decode the request body
	if err := json.NewDecoder(req.Body).Decode(&credentials); err != nil {
		http.Error(w, "Invalid input", http.StatusBadRequest)
		return
	}

	// Find user in the database
	err := r.DB.Where("email = ?", credentials.Email).First(userModel).Error
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{"message": "User not found", "data": nil})
		return
	}

	UserRoles := []models.UserRole{}
	if err := r.DB.Preload("Role").Where("user_id = ?", userModel.ID).Find(&UserRoles).Error; err != nil {
		http.Error(w, "Unable to retrieve user roles", http.StatusBadRequest)
		return
	}

	// Compare password with hashed password
	if err := bcrypt.CompareHashAndPassword([]byte(userModel.Password), []byte(credentials.Password)); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"message": "Invalid credentials", "data": nil})
		return
	}


	// Generate JWT token
	token, err := generateJWT(strconv.FormatUint(uint64(userModel.ID), 10))
	if err != nil {
		http.Error(w, "Error generating token", http.StatusBadRequest)
		return
	}

	// Respond with token
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"data":    userModel,
		"user_roles": UserRoles,
		"message": "Login successful",
		"token":   token,
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

	// Parse token to get expiration time
	claims := &jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_SECRET")), nil
	})

	if err != nil || !token.Valid {
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	exp, ok := (*claims)["exp"].(float64)
	if !ok {
		http.Error(w, "Invalid token claims", http.StatusUnauthorized)
		return
	}

	expirationTime := time.Unix(int64(exp), 0)

	// Store the token in the blacklist map
	blacklistMutex.Lock()
	blacklistedTokens[tokenString] = expirationTime
	blacklistMutex.Unlock()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Logout successful",
	})
}

func (r *Repository) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
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

		// Check if token is blacklisted
		blacklistMutex.Lock()
		expTime, exists := blacklistedTokens[tokenString]
		blacklistMutex.Unlock()

		if exists && time.Now().Before(expTime) {
			http.Error(w, "Token has been revoked", http.StatusUnauthorized)
			return
		}

		claims := &jwt.MapClaims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(os.Getenv("JWT_SECRET")), nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		userID, ok := (*claims)["user_id"].(string)
		if !ok {
			http.Error(w, "Invalid token claims", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), "user_id", userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func extractUserFromJWT(req *http.Request, db *gorm.DB) (*models.User, error) {
	tokenString := req.Header.Get("Authorization")
	if tokenString == "" {
		return nil, fmt.Errorf("missing token")
	}

	fmt.Println("Received Token:", tokenString) // Debug print

	// Ensure "Bearer " prefix is removed
	tokenString = strings.TrimPrefix(tokenString, "Bearer ")

	// Parse the token
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return []byte(os.Getenv("JWT_SECRET")), nil // Ensure you are using the correct secret key
	})
	if err != nil || !token.Valid {
		fmt.Println("JWT Parse Error:", err) // Debug print
		return nil, fmt.Errorf("invalid token")
	}

	// Extract claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		fmt.Println("Invalid Claims")
		return nil, fmt.Errorf("invalid claims")
	}

	fmt.Println("Extracted Claims:", claims) // Debug print

	// Extract user_id
	userID, ok := claims["user_id"].(string)
	if !ok {
		fmt.Println("Invalid user_id claim")
		return nil, fmt.Errorf("invalid user_id claim")
	}

	fmt.Println("Extracted User ID:", userID) // Debug print

	// Fetch user from DB
	var user models.User
	err = db.Preload("UserRoles").Where("id = ?", userID).First(&user).Error
	if err != nil {
		fmt.Println("User Not Found in DB:", err) // Debug print
		return nil, fmt.Errorf("user not found")
	}

	fmt.Println("Extracted User:", user) // Debug print
	return &user, nil
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

var blacklistedTokens = make(map[string]time.Time)
var blacklistMutex sync.Mutex

func (r *Repository) SetupRoutes(rts *mux.Router) {
	// Public routes
	rts.HandleFunc("/login", r.Login).Methods("POST")
	rts.HandleFunc("/create_user", r.CreateUser).Methods("POST")

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