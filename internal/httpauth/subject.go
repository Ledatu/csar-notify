package httpauth

import (
	"net/http"

	"github.com/google/uuid"
	csarerrors "github.com/ledatu/csar-core/errors"
	"github.com/ledatu/csar-core/gatewayctx"
)

func SubjectFromRequest(r *http.Request) (string, error) {
	id, ok := gatewayctx.FromContext(r.Context())
	if !ok || id.Subject == "" {
		return "", csarerrors.Unauthorized("missing gateway subject")
	}
	if _, err := uuid.Parse(id.Subject); err != nil {
		return "", csarerrors.Unauthorized("gateway subject must be a valid UUID")
	}
	return id.Subject, nil
}
