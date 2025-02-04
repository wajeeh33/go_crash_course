package main


import (
	"io"
	"log"
	"encoding/json"
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
	"time"
	"sort"
)

type Repository struct {
	DB *gorm.DB
}

type ByID []models.Book
func (b ByID) Len() int { return len(b) }
func (b ByID) Less(i, j int) bool { return b[i].ID < b[j].ID } // Ascending order
func (b ByID) Swap(i, j int)      { b[i], b[j] = b[j], b[i] }


// Helper function to write JSON responses
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

const MaxFileSize = 10 * 1024 * 1024 // 10 MB

func (r *Repository) GetBooks(w http.ResponseWriter, req *http.Request) {
	var bookModels []models.Book
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
	sort.Sort(sort.Reverse(ByID(bookModels)))
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
	// Parse the incoming multipart form data (including file upload)
	err := req.ParseMultipartForm(MaxFileSize)
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
	userRole := models.UserRole{UserID: userModel.ID, RoleID: "admin"} // Adjust this line
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

func (r *Repository) SetupRoutes(rts *mux.Router) {
	rts.HandleFunc("/create_books", r.CreateBook).Methods("POST")
	rts.HandleFunc("/delete_books/{id}", r.DeleteBook).Methods("DELETE")
	rts.HandleFunc("/update_books/{id}", r.UpdateBook).Methods("PUT")
	rts.HandleFunc("/books", r.GetBooks).Methods("GET")
	rts.HandleFunc("/get_books/{id}", r.GetBook).Methods("GET")
	rts.HandleFunc("/download_image/{id}", r.DownloadImage).Methods("GET")
	rts.HandleFunc("/create_user", r.CreateUser).Methods("POST")

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