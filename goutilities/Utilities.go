package goutilities

import (
	"math/rand"
	"net"
	"os"
	"time"
)

var r1 *rand.Rand

func init() {
	s1 := rand.NewSource(time.Now().UnixNano())
	r1 = rand.New(s1)
}

//RandomIntBetween gives a randon integer between two numbers
func RandomIntBetween(a int, b int) int {
	return r1.Intn(b-a) + a
}

//RandomInt gives a random int less than the number provided
func RandomInt(n int) int {
	return r1.Intn(n)
}

//RandomInt64 gives a 64bit random number
func RandomInt64() int64 {
	return r1.Int63()
}

//RandomUint64 gives an unsigned 64 bit random number
func RandomUint64() uint64 {
	return uint64(rand.Uint32())<<32 + uint64(rand.Uint32())
}

//RandomString gives a random string of length = parameter
func RandomString(strlen int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := ""
	for i := 0; i < strlen; i++ {
		index := r1.Intn(len(chars))
		result += chars[index : index+1]
	}
	return result
}

//RemoveDuplicates removes duplicates from the slice. It returns a new slice
func RemoveDuplicates(a []string) []string {
	result := []string{}
	seen := map[string]string{}
	for _, val := range a {
		if _, ok := seen[val]; !ok {
			result = append(result, val)
			seen[val] = val
		}
	}
	return result
}

// GetOutboundIP : returns preferred outbound ip of the current machine
func GetOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return ""
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP.String()
}

// GetHostName : Returns host name of the current machine
func GetHostName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
