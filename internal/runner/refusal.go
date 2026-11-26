package runner

import "time"

type Delayed interface{ RetryAt() time.Time }

type Terminal interface{ Final() bool }

type Explained interface{ Sentence() string }
