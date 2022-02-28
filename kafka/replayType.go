package kafka

// ReplayType is an enum which represents two types of
// kafka consumer replays. timestamp and beginning
type ReplayType int

const (
	// TIMESTAMP replays logs from a particular time
	TIMESTAMP ReplayType = iota
	// BEGINNING replays logs from the beginning
	BEGINNING
)

// String - Creating common behavior - give the type a String function
func (rf ReplayType) String() string {
	return [...]string{"timestamp", "beginning"}[rf]
}

// EnumIndex - Creating common behavior - give the type a EnumIndex function
func (rf ReplayType) EnumIndex() int {
	return int(rf)
}
