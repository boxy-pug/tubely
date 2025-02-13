package main

import (
	"fmt"
	"io"
	"log"
	"net/http"

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

	imgData, err := io.ReadAll(file)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "unable to read img data", err)
		return
	}

	vidMetaData, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error fetching video metadata", err)
		return
	}

	if vidMetaData.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "wrong user", err)
	}

	tn := thumbnail{
		data:      imgData,
		mediaType: header.Header.Get("Content-Type"),
	}

	videoThumbnails[videoID] = tn

	thumbnailUrl := fmt.Sprintf("http://localhost:%s/api/thumbnails/%v", cfg.port, videoID)
	log.Printf(thumbnailUrl)
	vidMetaData.ThumbnailURL = &thumbnailUrl

	err = cfg.db.UpdateVideo(vidMetaData)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "error updating video record", err)
		return
	}

	respondWithJSON(w, http.StatusOK, vidMetaData)
}
