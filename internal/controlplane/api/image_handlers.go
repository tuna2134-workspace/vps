package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

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
	KernelURL           string `json:"kernel_url"`
	InitrdURL           string `json:"initrd_url"`
	Cmdline             string `json:"cmdline"`
}

type ImageHandlers struct {
	images *images.Service
}

func NewImageHandlers(imageSvc *images.Service) *ImageHandlers {
	return &ImageHandlers{images: imageSvc}
}

func (h *ImageHandlers) List(c *gin.Context) {
	imgs, err := h.images.List(c.Request.Context(), c.Query("active") != "false")
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, imgs)
}

func (h *ImageHandlers) Get(c *gin.Context) {
	img, err := h.images.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		mapError(c, err)
		return
	}
	writeData(c, http.StatusOK, img)
}

func (h *ImageHandlers) Create(c *gin.Context) {
	var req imageRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	if req.Name == "" || req.Version == "" || req.SourceURL == "" {
		writeError(c, http.StatusBadRequest, "VALIDATION_ERROR", "name, version and source_url are required")
		return
	}
	created, err := h.images.Create(c.Request.Context(), &models.Image{
		Name:                req.Name,
		Version:             req.Version,
		Architecture:        req.Architecture,
		Format:              req.Format,
		SourceURL:           req.SourceURL,
		Checksum:            req.Checksum,
		SizeBytes:           req.SizeBytes,
		CloudInitCompatible: req.CloudInitCompatible,
		KernelURL:           req.KernelURL,
		InitrdURL:           req.InitrdURL,
		Cmdline:             req.Cmdline,
	})
	if err != nil {
		if err == images.ErrConflict {
			writeError(c, http.StatusConflict, "CONFLICT", "image already exists")
			return
		}
		mapError(c, err)
		return
	}
	writeData(c, http.StatusCreated, created)
}
