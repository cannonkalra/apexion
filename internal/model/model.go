// Package model defines the core domain vocabulary shared across every
// subsystem of Apexion (crawler, catalog, inference, lineage, api, ui).
//
// Types here are plain data. Behaviour lives in the services that own them.
package model

import (
	"time"
)

// ---------------------------------------------------------------------------
// Enumerations
// ---------------------------------------------------------------------------

// Format identifies a dataset storage format.
type Format string

const (
	FormatUnknown Format = "unknown"
	FormatCSV     Format = "csv"
	FormatTSV     Format = "tsv"
	FormatJSON    Format = "json"
	FormatJSONL   Format = "jsonl"
	FormatParquet Format = "parquet"
	FormatORC     Format = "orc"
	FormatAvro    Format = "avro"
	FormatIceberg Format = "iceberg"
	FormatDelta   Format = "delta"
)

// Compression identifies a file compression codec.
type Compression string

const (
	CompressionNone   Compression = "none"
	CompressionGzip   Compression = "gzip"
	CompressionSnappy Compression = "snappy"
	CompressionZstd   Compression = "zstd"
	CompressionBrotli Compression = "brotli"
	CompressionLZ4    Compression = "lz4"
	CompressionBzip2  Compression = "bzip2"
)

// DataType is the normalized logical type of a column, independent of source
// format. Inference maps every physical type onto one of these.
type DataType string

const (
	TypeUnknown   DataType = "unknown"
	TypeString    DataType = "string"
	TypeInteger   DataType = "integer"
	TypeFloat     DataType = "float"
	TypeDecimal   DataType = "decimal"
	TypeBoolean   DataType = "boolean"
	TypeDate      DataType = "date"
	TypeTimestamp DataType = "timestamp"
	TypeTime      DataType = "time"
	TypeUUID      DataType = "uuid"
	TypeJSON      DataType = "json"
	TypeArray     DataType = "array"
	TypeStruct    DataType = "struct"
	TypeMap       DataType = "map"
	TypeBinary    DataType = "binary"
	TypeNull      DataType = "null"
)

// SemanticType is a higher-level, business-meaning classification produced by
// the inference engine (a superset of DataType).
type SemanticType string

const (
	SemanticNone        SemanticType = ""
	SemanticEmail       SemanticType = "email"
	SemanticPhone       SemanticType = "phone"
	SemanticURL         SemanticType = "url"
	SemanticIPAddress   SemanticType = "ip_address"
	SemanticUUID        SemanticType = "uuid"
	SemanticName        SemanticType = "person_name"
	SemanticAddress     SemanticType = "postal_address"
	SemanticCountry     SemanticType = "country"
	SemanticCurrency    SemanticType = "currency"
	SemanticLanguage    SemanticType = "language"
	SemanticCreditCard  SemanticType = "credit_card"
	SemanticSSN         SemanticType = "ssn"
	SemanticZipCode     SemanticType = "zip_code"
	SemanticLatitude    SemanticType = "latitude"
	SemanticLongitude   SemanticType = "longitude"
	SemanticGender      SemanticType = "gender"
	SemanticDateTime    SemanticType = "datetime"
	SemanticIdentifier  SemanticType = "identifier"
	SemanticCategorical SemanticType = "categorical"
)

// RunStatus is the lifecycle state of a crawler / inference run or a job.
type RunStatus string

const (
	StatusQueued    RunStatus = "queued"
	StatusRunning   RunStatus = "running"
	StatusCompleted RunStatus = "completed"
	StatusFailed    RunStatus = "failed"
	StatusCancelled RunStatus = "cancelled"
)

// JobType classifies a background job.
type JobType string

const (
	JobCrawl     JobType = "crawl"
	JobInference JobType = "inference"
	JobLineage   JobType = "lineage"
)

// CrawlMode is how much of a bucket a crawl covers.
type CrawlMode string

const (
	CrawlFull        CrawlMode = "full"
	CrawlIncremental CrawlMode = "incremental"
)

// ScheduleKind is how a crawl is triggered.
type ScheduleKind string

const (
	ScheduleManual ScheduleKind = "manual"
	ScheduleCron   ScheduleKind = "cron"
)

// ---------------------------------------------------------------------------
// Core entities (normalized, one struct per table)
// ---------------------------------------------------------------------------

// Bucket is a top-level object-store container that Apexion crawls.
type Bucket struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Endpoint     string     `json:"endpoint"`
	Region       string     `json:"region"`
	ObjectCount  int64      `json:"object_count"`
	TotalSize    int64      `json:"total_size"`
	DatasetCount int64      `json:"dataset_count"`
	Schedule     string     `json:"schedule"` // cron expression, empty for manual
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastCrawlAt  *time.Time `json:"last_crawl_at,omitempty"`
}

// Object is a single physical object in a bucket (one S3 key).
type Object struct {
	ID           string      `json:"id"`
	BucketID     string      `json:"bucket_id"`
	BucketName   string      `json:"bucket_name"`
	Key          string      `json:"key"`
	ETag         string      `json:"etag"`
	VersionID    string      `json:"version_id"`
	Size         int64       `json:"size"`
	Format       Format      `json:"format"`
	Compression  Compression `json:"compression"`
	DatasetID    string      `json:"dataset_id"`
	IsHidden     bool        `json:"is_hidden"`
	MetadataHash string      `json:"metadata_hash"` // hash of (etag,size,modified) for change detection
	StorageClass string      `json:"storage_class"`
	CreatedAt    time.Time   `json:"created_at"`
	Modified     time.Time   `json:"modified"`
	DiscoveredAt time.Time   `json:"discovered_at"`
}

// Dataset is a logical grouping of objects that share a schema and layout
// (e.g. a Hive-partitioned table, a folder of CSVs, an Iceberg table).
type Dataset struct {
	ID            string      `json:"id"`
	BucketID      string      `json:"bucket_id"`
	BucketName    string      `json:"bucket_name"`
	Name          string      `json:"name"`
	Path          string      `json:"path"` // common prefix
	Format        Format      `json:"format"`
	Compression   Compression `json:"compression"`
	FileCount     int64       `json:"file_count"`
	TotalSize     int64       `json:"total_size"`
	RowCount      int64       `json:"row_count"` // estimated
	PartitionKeys []string    `json:"partition_keys"`
	SchemaID      string      `json:"schema_id"`
	Description   string      `json:"description"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
	LastScanAt    *time.Time  `json:"last_scan_at,omitempty"`
}

// Schema is a versioned set of columns for a dataset.
type Schema struct {
	ID          string    `json:"id"`
	DatasetID   string    `json:"dataset_id"`
	Version     int       `json:"version"`
	Fingerprint string    `json:"fingerprint"` // stable hash of column defs
	Columns     []Column  `json:"columns"`
	CreatedAt   time.Time `json:"created_at"`
}

// Column is one field in a schema.
type Column struct {
	ID           string       `json:"id"`
	SchemaID     string       `json:"schema_id"`
	Name         string       `json:"name"`
	Position     int          `json:"position"`
	DataType     DataType     `json:"data_type"`
	PhysicalType string       `json:"physical_type"` // as reported by the source format
	Nullable     bool         `json:"nullable"`
	SemanticType SemanticType `json:"semantic_type"`
	IsPartition  bool         `json:"is_partition"`
	Description  string       `json:"description"`
	Statistics   *Statistics  `json:"statistics,omitempty"`
}

// Statistics holds per-column profiling numbers.
type Statistics struct {
	ID            string   `json:"id"`
	ColumnID      string   `json:"column_id"`
	NullCount     int64    `json:"null_count"`
	DistinctCount int64    `json:"distinct_count"` // HyperLogLog estimate
	MinValue      string   `json:"min_value"`
	MaxValue      string   `json:"max_value"`
	MeanValue     *float64 `json:"mean_value,omitempty"`
	SampleCount   int64    `json:"sample_count"`
	Completeness  float64  `json:"completeness"` // 1 - null_ratio
	Uniqueness    float64  `json:"uniqueness"`   // distinct/count
	SampleValues  []string `json:"sample_values"`
}

// Partition is one Hive-style partition of a dataset.
type Partition struct {
	ID        string            `json:"id"`
	DatasetID string            `json:"dataset_id"`
	Path      string            `json:"path"`
	Values    map[string]string `json:"values"` // key=col -> value
	FileCount int64             `json:"file_count"`
	Size      int64             `json:"size"`
	RowCount  int64             `json:"row_count"`
	CreatedAt time.Time         `json:"created_at"`
}

// LineageNode is a vertex in the lineage graph.
type LineageNode struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"` // bucket|dataset|table|column|partition
	RefID     string    `json:"ref_id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// LineageEdge connects two lineage nodes.
type LineageEdge struct {
	ID        string    `json:"id"`
	FromID    string    `json:"from_id"`
	ToID      string    `json:"to_id"`
	Relation  string    `json:"relation"` // contains|derives|partitions
	CreatedAt time.Time `json:"created_at"`
}

// CrawlerRun records a single execution of the crawler.
type CrawlerRun struct {
	ID             string       `json:"id"`
	BucketID       string       `json:"bucket_id"`
	BucketName     string       `json:"bucket_name"`
	Mode           CrawlMode    `json:"mode"`
	Trigger        ScheduleKind `json:"trigger"`
	Status         RunStatus    `json:"status"`
	ObjectsScanned int64        `json:"objects_scanned"`
	ObjectsNew     int64        `json:"objects_new"`
	ObjectsChanged int64        `json:"objects_changed"`
	DatasetsFound  int64        `json:"datasets_found"`
	BytesScanned   int64        `json:"bytes_scanned"`
	Checkpoint     string       `json:"checkpoint"` // continuation token for resume
	Error          string       `json:"error"`
	StartedAt      time.Time    `json:"started_at"`
	FinishedAt     *time.Time   `json:"finished_at,omitempty"`
}

// InferenceRun records a single execution of the inference engine.
type InferenceRun struct {
	ID           string     `json:"id"`
	DatasetID    string     `json:"dataset_id"`
	DatasetName  string     `json:"dataset_name"`
	Status       RunStatus  `json:"status"`
	SampleRows   int64      `json:"sample_rows"`
	QualityScore float64    `json:"quality_score"`
	Findings     string     `json:"findings"` // JSON blob of InferenceResult
	Error        string     `json:"error"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// DataSample is a captured sample of rows for a dataset (as JSON).
type DataSample struct {
	ID        string    `json:"id"`
	DatasetID string    `json:"dataset_id"`
	RowsJSON  string    `json:"rows_json"`
	RowCount  int       `json:"row_count"`
	CreatedAt time.Time `json:"created_at"`
}

// Job is a unit of background work with progress tracking.
type Job struct {
	ID         string     `json:"id"`
	Type       JobType    `json:"type"`
	Status     RunStatus  `json:"status"`
	RefID      string     `json:"ref_id"` // bucket or dataset id
	Label      string     `json:"label"`
	Progress   float64    `json:"progress"` // 0..1
	Message    string     `json:"message"`
	Error      string     `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Connection is a stored object-store connection profile (one AWS/MinIO/S3
// account). Exactly one connection is active at a time.
type Connection struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Provider  string    `json:"provider"` // minio | aws | s3
	Endpoint  string    `json:"endpoint"`
	Region    string    `json:"region"`
	AccessKey string    `json:"access_key"`
	SecretKey string    `json:"secret_key"`
	UseSSL    bool      `json:"use_ssl"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Event is a persisted, structured domain event (see internal/events).
type Event struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Subject   string    `json:"subject"` // ref id the event is about
	Payload   string    `json:"payload"` // JSON
	CreatedAt time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Value objects used by inference (not persisted as separate tables)
// ---------------------------------------------------------------------------

// InferenceResult is the rich output of an inference run, serialized into
// InferenceRun.Findings.
type InferenceResult struct {
	DatasetID      string                `json:"dataset_id"`
	SampleRows     int                   `json:"sample_rows"`
	Columns        []ColumnInference     `json:"columns"`
	PrimaryKeys    []string              `json:"primary_keys"`
	ForeignKeys    []ForeignKeyCandidate `json:"foreign_keys"`
	QualityScore   float64               `json:"quality_score"`
	QualityMetrics QualityMetrics        `json:"quality_metrics"`
	PIIColumns     []string              `json:"pii_columns"`
	GeneratedAt    time.Time             `json:"generated_at"`
	LLMSummary     string                `json:"llm_summary,omitempty"`

	// Extended analyses (Phase 1).
	BusinessDescription   string             `json:"business_description,omitempty"`
	DatasetSummary        string             `json:"dataset_summary,omitempty"`
	RecommendedPartitions []string           `json:"recommended_partitions"`
	DuplicateAnalysis     DuplicateAnalysis  `json:"duplicate_analysis"`
	MissingValues         []MissingValueStat `json:"missing_values"`
	DorisSchema           string             `json:"doris_schema,omitempty"`
	SparkOptimizations    []string           `json:"spark_optimizations"`
	FlinkOptimizations    []string           `json:"flink_optimizations"`
}

// DuplicateAnalysis summarizes duplicate rows in the sample.
type DuplicateAnalysis struct {
	TotalRows      int     `json:"total_rows"`
	DuplicateRows  int     `json:"duplicate_rows"`
	DuplicateRatio float64 `json:"duplicate_ratio"`
	UniqueRows     int     `json:"unique_rows"`
}

// MissingValueStat is a per-column null summary.
type MissingValueStat struct {
	Column    string  `json:"column"`
	NullCount int     `json:"null_count"`
	NullRatio float64 `json:"null_ratio"`
}

// ColumnInference is the per-column result of inference.
type ColumnInference struct {
	Name          string       `json:"name"`
	DataType      DataType     `json:"data_type"`
	SemanticType  SemanticType `json:"semantic_type"`
	Nullable      bool         `json:"nullable"`
	IsPII         bool         `json:"is_pii"`
	IsPKCandidate bool         `json:"is_pk_candidate"`
	Completeness  float64      `json:"completeness"`
	Uniqueness    float64      `json:"uniqueness"`
	DistinctCount int64        `json:"distinct_count"`
	MinValue      string       `json:"min_value"`
	MaxValue      string       `json:"max_value"`
	SampleValues  []string     `json:"sample_values"`
	DateFormat    string       `json:"date_format,omitempty"`
	Confidence    float64      `json:"confidence"`
}

// ForeignKeyCandidate is a suspected FK relationship.
type ForeignKeyCandidate struct {
	Column       string  `json:"column"`
	RefDataset   string  `json:"ref_dataset"`
	RefColumn    string  `json:"ref_column"`
	OverlapRatio float64 `json:"overlap_ratio"`
	Confidence   float64 `json:"confidence"`
}

// QualityMetrics summarizes dataset-level data quality.
type QualityMetrics struct {
	Completeness float64 `json:"completeness"` // avg non-null ratio
	Uniqueness   float64 `json:"uniqueness"`   // avg distinct ratio
	Validity     float64 `json:"validity"`     // fraction of values matching inferred type
	Consistency  float64 `json:"consistency"`  // fraction of columns with a single stable type
	Rows         int     `json:"rows"`
	Columns      int     `json:"columns"`
}
