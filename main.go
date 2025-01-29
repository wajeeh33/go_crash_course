package main


import (
	"io"
	"log"
	"encoding/json"
	"fmt"
	"github.com/gorilla/mux"
	"github.com/joho/godotenv"
	"github.com/gorilla/schema"
	"net/http"
	"gorm.io/gorm"
	"github.com/wajeeh33/go_crash_course/storage"
	"github.com/wajeeh33/go_crash_course/models"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Book struct {
	Title string `json:"title"`
	Author string `json:"author"`
	Publisher string `json:"publisher"`
	ImagePath string `json:"image_path"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt time.Time `json:"updated_at" gorm:"autoCreateTime"`
}

type Repository struct {
	DB *gorm.DB
}


// Helper function to write JSON responses
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

const MaxFileSize = 10 * 1024 * 1024 // 10 MB

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
			http.Error(w, "Failed to create upload directory", http.StatusInternalServerError)
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
		http.Error(w, "Error while saving image", http.StatusInternalServerError)
		return
	}
	defer out.Close()
	_, err = io.Copy(out, file)
	if err != nil {
		http.Error(w, "Error while saving image", http.StatusInternalServerError)
		return
	}

	// Store the image path in the book record (assuming you have an `ImagePath` field in the `Book` model)
	book.ImagePath = &imagePath

	// Save book to the database
	if err := r.DB.Create(book).Error; err != nil {
		http.Error(w, "Unable to create book", http.StatusInternalServerError)
		return
	}

	// Respond with success
	writeJSON(w, http.StatusCreated, map[string]interface{}{"message": "Book created successfully", "data": book})
}



func (r *Repository) GetBooks(w http.ResponseWriter, req *http.Request)  {
	bookModels := &[]models.Book{}
	err := r.DB.Find(bookModels)
	if err.Error != nil {
		http.Error(w, "Unable to fetch books", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "books fetched successfully", "data": bookModels})
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
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"message": "book fetched successfully", "data": bookModel})
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
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// Delete book and check if deletion was successful
	if r.DB.Delete(bookModel).RowsAffected == 0 {
		http.Error(w, "Unable to delete book", http.StatusInternalServerError)
		return
	}

	// Success response
	writeJSON(w, http.StatusOK, map[string]string{"message": "Book deleted successfully"})
}

func (r *Repository) UpdateBook(w http.ResponseWriter, req *http.Request) {
	bookModel := &models.Book{}
	vars := mux.Vars(req)

	// Extract both ID and author from the URL parameters
	id, idExists := vars["id"]
	author, authorExists := vars["author"]

	// Check if both id and author exist and are not empty
	if !idExists || id == "" || !authorExists || author == "" {
		http.Error(w, "Book ID and Author are required", http.StatusBadRequest)
		return
	}

	// Check if the book exists in the database
	err := r.DB.Where("id = ?", id).First(bookModel).Error
	if err != nil {
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// Decode the incoming JSON request body
	if err := json.NewDecoder(req.Body).Decode(&bookModel); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
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
		http.Error(w, "Book not found", http.StatusNotFound)
		return
	}

	// Check if ImagePath is set
	if bookModel.ImagePath == nil || *bookModel.ImagePath == "" {
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}

	// Set the header to indicate that we're serving a file
	w.Header().Set("Content-Type", "image/jpeg") // Change if necessary based on your image type
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filepath.Base(*bookModel.ImagePath)))

	// Serve the image file
	http.ServeFile(w, req, *bookModel.ImagePath)
}


func (r *Repository) SetupRoutes(rts *mux.Router) {
	rts.HandleFunc("/create_books", r.CreateBook).Methods("POST")
	rts.HandleFunc("/delete_books/{id}", r.DeleteBook).Methods("DELETE")
	rts.HandleFunc("/update_books/{id}", r.UpdateBook).Methods("PUT")
	rts.HandleFunc("/books", r.GetBooks).Methods("GET")
	rts.HandleFunc("/get_books/{id}", r.GetBook).Methods("GET")
	rts.HandleFunc("/download_image/{id}", r.DownloadImage).Methods("GET")

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

	err = models.MigrateBook(db)
	if err != nil {
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