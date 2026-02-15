module dashwatch

go 1.22.10

require (
	dash v0.0.0
	github.com/fsnotify/fsnotify v1.9.0
	github.com/lib/pq v1.10.9
)

require (
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/sys v0.27.0 // indirect
)

replace dash => ../..
