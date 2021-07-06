package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
)

var (
	// redisClient is a go-redis client used to write the in-memory RepoCache to a DB,
	// allowing for some persistence of data in case this server crashes
	RedisClient *redis.Client
	ctx         = context.Background()
)

const (
	REDISNIL = "redis: nil"
)

func InitRedisClient(redisAddr string) {
	log.Infof("Redis addr is: %s", redisAddr)
	RedisClient = redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: "",
		DB:       0, //default DB
	})
	// verify that app can reach Redis
	status := RedisClient.Ping(ctx)
	log.Infof("🏓 Result of redis ping: %v", status)
}

// ---- GET FROM REDIS --------
// given Repos, and an existing redis DB, get any cached values from Redis
// and reconcile with the builds currently in Repos.
// (runs only at startup)
func GetFromRedisAndReconcile() error {
	log.Info("Attempting to get from Redis...")
	if len(Repos) == 0 {
		return fmt.Errorf("Repos is empty, cannot reconcile from Redis")
	}
	for i, r := range Repos {
		m, err := RedisClient.Get(ctx, r.Link).Result()
		if err != nil {
			if err.Error() == REDISNIL {
				log.Warnf("No Redis builds found for repo: %s, continuing.", r.Link)
				continue
			} else {
				return err
			}
		}
		b := []byte(m)
		var redisBuilds map[string]Build
		err = json.Unmarshal(b, &redisBuilds)
		if err != nil {
			return err
		}
		log.Debugf("Got Redis builds for repo: %s - %v", r.Link, redisBuilds)
		for redisDatefmt, redisBuild := range redisBuilds {
			for j, b := range r.Builds {
				if redisDatefmt == b.Datefmt {
					Repos[i].Builds[j] = redisBuild
					log.Debugf("Repo %s - Overrode Repo build for %s with Redis build at sha: %s", r.Link, b.Datefmt, redisBuild.Sha)
				}
			}
		}
	}
	log.Info("♦️ Successfully got from Redis and reconciled with Repos")
	return nil
}

// ----- WRITE TO REDIS -----------
// runs on a ticker - inserts the latest builds for each Repo
// Key: `repo-link`
// Value: a transformed version of the Repo struct - map<date>Build
func UpdateRedis() error {
	for _, r := range Repos {
		key := r.Link
		val := map[string]Build{}
		for _, b := range r.Builds {
			val[b.Datefmt] = b
		}
		// marshal map into JSON
		m, err := json.Marshal(val)
		if err != nil {
			return err
		}

		// write key, value to redis client
		err = RedisClient.Set(ctx, key, m, 0).Err()
		if err != nil {
			return err
		}
	}
	log.Info("Successfully updated Redis with latest Builds.")
	return nil
}
