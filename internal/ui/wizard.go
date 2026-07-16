package ui

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/apexion/apexion/internal/catalog"
	"github.com/apexion/apexion/internal/model"
)

// wizardProgressURL builds the (properly query-encoded) crawl-progress poll URL
// so bucket/prefix values with spaces or reserved characters can't break it.
func wizardProgressURL(vm WizardVM) string {
	q := url.Values{"job": {vm.JobID}, "bucket": {vm.Bucket}, "prefix": {vm.Prefix}}
	return "/ui/wizard/progress?" + q.Encode()
}

// wizardSkipVals builds the hx-vals JSON for the Skip action's htmx POST. Using
// a standalone button (not a nested <form>) keeps the register form valid HTML —
// nested forms are reparented by browsers, breaking layout and submission.
func wizardSkipVals(datasetID string) string {
	b, _ := json.Marshal(map[string]string{"dataset_id": datasetID})
	return string(b)
}

// wizardPage renders the full-page Crawl → Analyze → Review → Register wizard,
// starting at step 1 (source selection). Launched from the Explorer.
func (h *Handler) wizardPage(w http.ResponseWriter, r *http.Request) {
	vm := WizardVM{
		Bucket: r.URL.Query().Get("bucket"),
		Prefix: r.URL.Query().Get("prefix"),
	}
	h.render(w, r, WizardPage(vm))
}

// wizardCrawl (step 1 → 2) starts the crawl job and returns the progress step.
func (h *Handler) wizardCrawl(w http.ResponseWriter, r *http.Request) {
	bucket := r.FormValue("bucket")
	prefix := r.FormValue("prefix")
	if bucket == "" {
		h.render(w, r, WizardStep1(WizardVM{Prefix: prefix, Error: "Bucket is required"}))
		return
	}
	mode := model.CrawlFull
	job := h.catalog.StartCrawlPrefix(bucket, prefix, mode, model.ScheduleManual)
	h.render(w, r, WizardStep2Progress(WizardVM{Bucket: bucket, Prefix: prefix, JobID: job.ID, Job: job}))
}

// wizardProgress (step 2 poll) reports crawl progress; when the job completes it
// resolves the discovered dataset and advances to the review step.
func (h *Handler) wizardProgress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	jobID, bucket, prefix := q.Get("job"), q.Get("bucket"), q.Get("prefix")
	job, err := h.store.GetJob(ctx, jobID)
	if err != nil || job == nil {
		h.render(w, r, WizardError("Crawl job not found."))
		return
	}
	vm := WizardVM{Bucket: bucket, Prefix: prefix, JobID: jobID, Job: job}
	switch job.Status {
	case model.StatusCompleted:
		ds, _ := h.catalog.FindDatasetByLocation(ctx, bucket, prefix)
		if ds == nil {
			h.render(w, r, WizardError("The crawl finished but discovered no dataset under this directory."))
			return
		}
		detail, _ := h.catalog.DatasetDetail(ctx, ds.ID)
		if detail == nil {
			h.render(w, r, WizardError("The discovered dataset could not be loaded."))
			return
		}
		vm.Detail = detail
		vm.Suggested = h.catalog.SuggestTableName(ctx, ds.Name)
		h.render(w, r, WizardStep3(vm))
	case model.StatusFailed, model.StatusCancelled:
		h.render(w, r, WizardError("Crawl "+string(job.Status)+": "+job.Error))
	default:
		h.render(w, r, WizardStep2Progress(vm)) // keeps polling
	}
}

// wizardRegisterForm (step 3 → 4) shows the registration form.
func (h *Handler) wizardRegisterForm(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("dataset_id")
	detail, err := h.catalog.DatasetDetail(r.Context(), id)
	if err != nil || detail == nil {
		h.render(w, r, WizardError("Dataset not found."))
		return
	}
	h.render(w, r, WizardStep4(WizardVM{
		Detail:    detail,
		Suggested: h.catalog.SuggestTableName(r.Context(), detail.Dataset.Name),
	}))
}

// wizardRegister (step 4 → 5) registers the dataset as a table, then shows the
// finish step. Registration is transactional (see catalog.RegisterDatasetWith).
func (h *Handler) wizardRegister(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("dataset_id")
	detail, err := h.catalog.DatasetDetail(r.Context(), id)
	if err != nil || detail == nil {
		h.render(w, r, WizardError("Dataset not found."))
		return
	}
	entry, err := h.catalog.RegisterDatasetWith(r.Context(), id, catalog.RegisterOptions{
		Name:           r.FormValue("name"),
		Description:    r.FormValue("description"),
		SchemaStrategy: r.FormValue("schema_strategy"),
		ReadOptions:    readOptionsFromForm(r),
	})
	if err != nil {
		h.render(w, r, WizardStep4(WizardVM{
			Detail: detail, Suggested: r.FormValue("name"), Error: err.Error(),
		}))
		return
	}
	h.render(w, r, WizardStep5(WizardVM{Detail: detail, Entry: entry}))
}

// readOptionsFromForm parses the "Read options" fieldset of the registration
// form into model.ReadOptions. An unchecked HTML checkbox sends no value, so a
// plain =="on" test yields false when unchecked and true when checked. An empty
// sample size means the engine default; header defaults to "auto".
func readOptionsFromForm(r *http.Request) model.ReadOptions {
	header := r.FormValue("opt_header")
	if header == "" {
		header = "auto"
	}
	sampleSize := 0
	if v := r.FormValue("opt_sample_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			sampleSize = n
		}
	}
	return model.ReadOptions{
		Filename:     r.FormValue("opt_filename") == "on",
		UnionByName:  r.FormValue("opt_union") == "on",
		Header:       header,
		SampleSize:   sampleSize,
		IgnoreErrors: r.FormValue("opt_ignore_errors") == "on",
	}
}

// wizardSkip (step 3 → 5) finishes without registering.
func (h *Handler) wizardSkip(w http.ResponseWriter, r *http.Request) {
	detail, _ := h.catalog.DatasetDetail(r.Context(), r.FormValue("dataset_id"))
	h.render(w, r, WizardStep5(WizardVM{Detail: detail}))
}

// wizardJobMessage renders a human progress line for a crawl job.
func wizardJobMessage(j *model.Job) string {
	if j == nil {
		return "Starting…"
	}
	if j.Message != "" {
		return j.Message
	}
	return "Crawling…"
}
