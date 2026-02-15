module dashhook

go 1.22.10

require (
	dash v0.0.0
	github.com/lib/pq v1.10.9
)

require github.com/google/uuid v1.6.0 // indirect

replace dash => ../..
