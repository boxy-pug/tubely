package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	maxMemory := 10 << 20
	r.ParseMultipartForm(int64(maxMemory))

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "unable to parse file", err)
		return
	}
	defer file.Close()

	/*
		imgData, err := io.ReadAll(file)
		if err != nil {
			respondWithError(w, http.StatusInternalServerError, "unable to read img data", err)
			return
		}
	*/

	fileExtension, err := determineFileExtension(header.Header.Get("Content-Type"))
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not determine file ext", err)
		return
	}

	thumbnailName, err := createRandomFileName()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not create thumbnail filename", err)
	}

	thumbnailFilePath := filepath.Join(cfg.assetsRoot, fmt.Sprintf("%s.%s", thumbnailName, fileExtension))

	outFile, err := os.Create(thumbnailFilePath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not create file", err)
		return
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, file); err != nil {
		respondWithError(w, http.StatusInternalServerError, "could not copy file", err)
	}

	vidMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error fetching video metadata", err)
		return
	}

	if vidMetaData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "wrong user", err)
	}

	thumbnailUrl := fmt.Sprintf("http://localhost:%s/assets/%s.%s", cfg.port, thumbnailName, fileExtension)

	vidMetaData.ThumbnailURL = &thumbnailUrl

	/*
		tn := thumbnail{
			data:      imgData,
			mediaType: header.Header.Get("Content-Type"),
		}
	*/
	// videoThumbnails[videoID] = tn

	// data := []byte(imgData)

	//fmt.Sprintf("/assets/%v.%v", videoID, fileExtension)

	// strData := base64.StdEncoding.EncodeToString(data)
	// thumbnailUrl := fmt.Sprintf("data:image/png;base64,%v", strData)

	// log.Printf(thumbnailFilePath)
	// vidMetaData.ThumbnailURL = &thumbnailFilePath

	err = cfg.db.UpdateVideo(vidMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error updating video record", err)
		return
	}

	respondWithJSON(w, http.StatusOK, vidMetaData)
}

func determineFileExtension(mediaType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "image/png":
		return "png", nil
	case "image/jpg", "image/jpeg":
		return "jpg", nil
	case "video/mp4":
		return "mp4", nil
	}
	return "", fmt.Errorf("error determining file ext")
}

func createRandomFileName() (string, error) {
	key := make([]byte, 32)
	rand.Read(key)
	newName := base64.RawURLEncoding.EncodeToString(key)
	return newName, nil
}
