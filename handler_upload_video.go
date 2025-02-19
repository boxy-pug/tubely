package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/database"
	"github.com/google/uuid"
)

type Stream struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type FFProbeOutput struct {
	Streams []Stream `json:"streams"`
}

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

	processedFilePath, err := processVideoForFastStart(tempFile.Name())
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to process video for fast start", err)
		return
	}
	defer os.Remove(processedFilePath)

	// get aspect ratio
	aspectRatio, err := getVideoAspectRatio(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to get video aspect ratio", err)
		return
	}

	// Generate random key
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to generate storage key", err)
		return
	}
	key := fmt.Sprintf("%s/%s.mp4", aspectRatio, hex.EncodeToString(keyBytes))

	processedFile, err := os.Open(processedFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to open processed file", err)
		return
	}
	defer processedFile.Close()

	// Upload to S3
	_, err = cfg.s3Client.PutObject(r.Context(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &key,
		Body:        processedFile,
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
	// videoURL := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, key)
	videoURL := fmt.Sprintf("%s,%s", cfg.s3Bucket, key)

	vidMetaData.VideoURL = &videoURL

	err = cfg.db.UpdateVideo(vidMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to update video URL", err)
		return
	}

	signedVideo, err := cfg.dbVideoToSignedVideo(vidMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "failed to generate presigned URL", err)
		return
	}

	respondWithJSON(w, http.StatusOK, signedVideo)

}

func getVideoAspectRatio(filePath string) (string, error) {
	// Run ffprobe command
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var out bytes.Buffer
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run ffprobe: %w", err)
	}

	// Parse JSON output
	var ffprobeOutput FFProbeOutput
	if err := json.Unmarshal(out.Bytes(), &ffprobeOutput); err != nil {
		return "", fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	if len(ffprobeOutput.Streams) == 0 {
		return "", fmt.Errorf("no streams found in video")
	}

	// Get width and height
	width := ffprobeOutput.Streams[0].Width
	height := ffprobeOutput.Streams[0].Height

	// Determine aspect ratio
	ratio := float64(width) / float64(height)
	if ratio > 1.7 && ratio < 1.8 {
		// 16:9
		return "landscape", nil
	} else if ratio > 0.55 && ratio < 0.57 {
		// 9:16
		return "portrait", nil
	} else {
		return "other", nil
	}
}

func processVideoForFastStart(filePath string) (string, error) {
	outputFilePath := filePath + ".processing"

	// Prepare the ffmpeg command
	cmd := exec.Command("ffmpeg", "-i", filePath, "-c", "copy", "-movflags", "faststart", "-f", "mp4", outputFilePath)

	// Run the command
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to process video for fast start: %w", err)
	}

	return outputFilePath, nil
}

func generatePresignedURL(s3Client *s3.Client, bucket, key string, expireTime time.Duration) (string, error) {
	presignClient := s3.NewPresignClient(s3Client)

	req, err := presignClient.PresignGetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	}, s3.WithPresignExpires(expireTime))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	return req.URL, nil
}

func (cfg *apiConfig) dbVideoToSignedVideo(video database.Video) (database.Video, error) {
	if video.VideoURL == nil {
		return video, fmt.Errorf("video URL is nil")
	}

	parts := strings.Split(*video.VideoURL, ",")
	if len(parts) != 2 {
		return video, fmt.Errorf("invalid video URL format")
	}

	bucket, key := parts[0], parts[1]
	presignedURL, err := generatePresignedURL(cfg.s3Client, bucket, key, 15*time.Minute)
	if err != nil {
		return video, fmt.Errorf("failed to generate presigned URL: %w", err)
	}

	video.VideoURL = &presignedURL
	return video, nil
}
