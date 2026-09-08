package api

import (
	"net/http"

	"github.com/tuna2134/vps/internal/controlplane/images"
	"github.com/tuna2134/vps/internal/controlplane/models"
)

type imageRequest struct {
	Name                string `json:"name"`
	Version             string `json:"version"`
	Architecture        string `json:"architecture"`
	Format              string `json:"format"`
	SourceURL           string `json:"source_url"`
	Checksum            string `json:"checksum"`
	SizeBytes           int64  `json:"size_bytes"`
	CloudInitCompatible bool   `json:"cloud_init_compatible"`
}

type ImageHandlers struct {
	images *images.Service
}

func NewImageHandlers(imageSvc *images.Service) *ImageHandlers {
	return &ImageHandlers{images: imageSvc}
}

func (h *ImageHandlers) List(w http.ResponseWriter, r *http.Request) {
	imgs, err := h.images.List(r.Context(), r.URL.Query().Get("active") != "false")
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, imgs)
}

func (h *ImageHandlers) Get(w http.ResponseWriter, r *http.Request) {
	img, err := h.images.Get(r.Context(), pathSegment(r, "/v1/images/"))
	if err != nil {
		mapError(w, err)
		return
	}
	writeData(w, http.StatusOK, img)
}

func (h *ImageHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req imageRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" || req.Version == "" || req.SourceURL == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name, version and source_url are required")
		return
	}
	created, err := h.images.Create(r.Context(), &models.Image{
		Name:                req.Name,
		Version:             req.Version,
		Architecture:        req.Architecture,
		Format:              req.Format,
		SourceURL:           req.SourceURL,
		Checksum:            req.Checksum,
		SizeBytes:           req.SizeBytes,
		CloudInitCompatible: req.CloudInitCompatible,
	})
	if err != nil {
		if err == images.ErrConflict {
			writeError(w, http.StatusConflict, "CONFLICT", "image already exists")
			return
		}
		mapError(w, err)
		return
	}
	writeData(w, http.StatusCreated, created)
}
