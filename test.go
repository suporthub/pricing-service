package main
import (
"fmt"
"github.com/redis/go-redis/v9"
)
func main() {
var i redis.ClientInfo
fmt.Printf("%+v\n", i)
}
