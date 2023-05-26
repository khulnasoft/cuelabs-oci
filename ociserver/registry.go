// Copyright 2018 Google LLC All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package ociregistry implements a docker V2 registry and the OCI distribution specification.
//
// It is designed to be used anywhere a low dependency container registry is needed, with an
// initial focus on tests.
//
// Its goal is to be standards compliant and its strictness will increase over time.
//
// This is currently a low flightmiles system. It's likely quite safe to use in tests; If you're using it
// in production, please let us know how and send us CL's for integration tests.
package ociserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"

	ocispecroot "github.com/opencontainers/image-spec/specs-go"
	"github.com/rogpeppe/ociregistry"
	"github.com/rogpeppe/ociregistry/internal/ocirequest"
)

// debug causes debug messages to be emitted when running the server.
const debug = false

var v2 = ocispecroot.Versioned{
	SchemaVersion: 2,
}

// Options holds options for the server.
type Options struct {
	// DisableReferrersAPI, when true, causes the registry to behave as if
	// it does not understand the referrers API.
	DisableReferrersAPI bool

	// LocationForUploadID transforms an upload ID as returned by
	// ocirequest.BlobWriter.ID to the absolute URL location
	// as returned by the upload endpoints.
	//
	// By default, when this function is nil, or it returns an empty
	// string, upload IDs are treated as opaque identifiers and the
	// returned locations are always host-relative URLs into the
	// server itself.
	//
	// This can be used to allow clients to fetch and push content
	// directly from some upstream server rather than passing
	// through this server. Clients doing that will need access
	// rights to that remote location.
	LocationForUploadID func(string) (string, error)

	DebugID string
}

var debugID int32

// New returns a handler which implements the docker registry protocol
// by making calls to the underlying registry backend r.
//
// If opts is nil, it's equivalent to passing new(Options).
//
// The returned handler should be registered at the site root.
func New(backend ociregistry.Interface, opts *Options) http.Handler {
	if opts == nil {
		opts = new(Options)
	}
	if opts.DebugID == "" {
		opts.DebugID = fmt.Sprintf("ociserver%d", atomic.AddInt32(&debugID, 1))
	}
	return &registry{
		backend:          backend,
		referrersEnabled: !opts.DisableReferrersAPI,
		debugID:          opts.DebugID,
	}
}

func (r *registry) logf(f string, a ...any) {
	log.Printf("ociserver %s: %s", r.debugID, fmt.Sprintf(f, a...))
}

type registry struct {
	backend          ociregistry.Interface
	referrersEnabled bool
	debugID          string
}

var handlers = []func(r *registry, ctx context.Context, w http.ResponseWriter, req *http.Request, rreq *ocirequest.Request) error{
	ocirequest.ReqPing:               (*registry).handlePing,
	ocirequest.ReqBlobGet:            (*registry).handleBlobGet,
	ocirequest.ReqBlobHead:           (*registry).handleBlobHead,
	ocirequest.ReqBlobDelete:         (*registry).handleBlobDelete,
	ocirequest.ReqBlobStartUpload:    (*registry).handleBlobStartUpload,
	ocirequest.ReqBlobUploadBlob:     (*registry).handleBlobUploadBlob,
	ocirequest.ReqBlobMount:          (*registry).handleBlobMount,
	ocirequest.ReqBlobUploadInfo:     (*registry).handleBlobUploadInfo,
	ocirequest.ReqBlobUploadChunk:    (*registry).handleBlobUploadChunk,
	ocirequest.ReqBlobCompleteUpload: (*registry).handleBlobCompleteUpload,
	ocirequest.ReqManifestGet:        (*registry).handleManifestGet,
	ocirequest.ReqManifestHead:       (*registry).handleManifestHead,
	ocirequest.ReqManifestPut:        (*registry).handleManifestPut,
	ocirequest.ReqManifestDelete:     (*registry).handleManifestDelete,
	ocirequest.ReqTagsList:           (*registry).handleTagsList,
	ocirequest.ReqReferrersList:      (*registry).handleReferrersList,
	ocirequest.ReqCatalogList:        (*registry).handleCatalogList,
}

func (r *registry) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	if rerr := r.v2(resp, req); rerr != nil {
		writeError(resp, rerr)
		return
	}
}

// https://docs.docker.com/registry/spec/api/#api-version-check
// https://github.com/opencontainers/distribution-spec/blob/master/spec.md#api-version-check
func (r *registry) v2(resp http.ResponseWriter, req *http.Request) (_err error) {
	if debug {
		r.logf("registry.v2 %v %s {", req.Method, req.URL)
		defer func() {
			if _err != nil {
				r.logf("} -> %v", _err)
			} else {
				r.logf("}")
			}
		}()
	}

	rreq, err := ocirequest.Parse(req.Method, req.URL)
	if err != nil {
		resp.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		return handlerErrorForRequestParseError(err)
	}
	handle := handlers[rreq.Kind]
	return handle(r, req.Context(), resp, req, rreq)
}

func (r *registry) handlePing(ctx context.Context, resp http.ResponseWriter, req *http.Request, rreq *ocirequest.Request) error {
	resp.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	return nil
}

// ParseError represents an error that can happen when parsing.
// The Err field holds one of the possible error values below.
type ParseError struct {
	error
}

func handlerErrorForRequestParseError(err error) error {
	if err == nil {
		return nil
	}
	var perr *ocirequest.ParseError
	if !errors.As(err, &perr) {
		return err
	}
	switch perr.Err {
	case ocirequest.ErrNotFound:
		return withHTTPCode(http.StatusNotFound, err)
	case ocirequest.ErrBadlyFormedDigest:
		return withHTTPCode(http.StatusBadRequest, err)
	case ocirequest.ErrMethodNotAllowed:
		return withHTTPCode(http.StatusMethodNotAllowed, err)
	case ocirequest.ErrBadRequest:
		return withHTTPCode(http.StatusBadRequest, err)
	}
	return err
}
