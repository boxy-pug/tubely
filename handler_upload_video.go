package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	// set upload limit of 1 gb
	r.Body = http.MaxBytesReader(w, r.Body, 1<<30)
	// get video id from path
	strVideoID := r.PathValue("videoID")
	videoID, err := uuid.Parse(strVideoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error parsing videoID as uuid", err)
		return
	}
	// get token
	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}
	// validate token and get userID
	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}
	// get video metadata
	vidMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error fetching video metadata", err)
		return
	}
	// if user is not video owner, unauthorize
	if userID != vidMetaData.UserID {
		respondWithError(w, http.StatusUnauthorized, "user is not video owner", err)
		return
	}
	// Parse the uploaded video file from the form data
	err = r.ParseMultipartForm(10 << 20)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "invalid form data", err)
		return
	}
	// get file in memory
	file, header, err := r.FormFile("video")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to parse file", err)
		return
	}
	defer file.Close()
	// validate file type
	contentType := header.Header.Get("Content-Type")
	mediatype, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediatype != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "invalidfile format", err)
	}
	// Create temporary file
	tempFile, err := os.CreateTemp("", "tubely-upload-*.mp4")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to create temp file", err)
		return
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Copy to temp file
	if _, err := io.Copy(tempFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to save video", err)
		return
	}

	// Reset file pointer
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to process video", err)
		return
	}

	// Generate random key
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to generate storage key", err)
		return
	}
	key := hex.EncodeToString(keyBytes) + ".mp4"

	// Upload to S3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        tempFile,
		ContentType: &contentType,
	})
	if err != nil {
		log.Printf("Attempting upload to bucket: '%s' in region: '%s' with key: '%s'",
			cfg.s3Bucket,
			cfg.s3Region,
			key)
		respondWithError(w, http.StatusInternalServerError, "failed to upload to cloud storage", err)
		return
	}

	// Update database with S3 URL
	videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s",
		cfg.s3Bucket, cfg.s3Region, key)

	vidMetaData.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(vidMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to update video URL", err)
		return
	}
	respondWithJSON(w, http.StatusOK, struct {
		Message string `json:"message"`
	}{
		Message: "Video uploaded successfully",
	})

}
