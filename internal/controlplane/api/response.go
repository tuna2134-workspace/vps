package api

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
)

// envelope is the consistent API response wrapper: {"data": ...}.
type envelope struct {
	Data any `json:"data"`
}

// errorBody is the consistent error shape: {"error": {code, message}}.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeData wraps the payload in the standard data envelope.
func writeData(c *gin.Context, status int, data any) {
	c.JSON(status, envelope{Data: data})
}

// writeError returns a structured error. Internal details are never leaked to
// the client.
func writeError(c *gin.Context, status int, code, message string) {
	c.JSON(status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// writeEmpty returns 204.
func writeEmpty(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// decodeJSON reads and strictly validates a JSON request body.
func decodeJSON(c *gin.Context, v any) error {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
