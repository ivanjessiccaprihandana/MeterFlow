package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

func Seed(ctx context.Context, db *pgxpool.Pool) error {
	hash, err := bcrypt.GenerateFromPassword([]byte("demo-password"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	org := "10000000-0000-4000-8000-000000000001"
	user := "20000000-0000-4000-8000-000000000001"
	starter := "30000000-0000-4000-8000-000000000001"
	pro := "30000000-0000-4000-8000-000000000002"
	_, err = tx.Exec(ctx, `INSERT INTO organizations(id,name,slug) VALUES($1,'Acme Demo','acme') ON CONFLICT DO NOTHING`, org)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,display_name,is_platform_admin) VALUES($1,'owner@acme.test',$2,'Demo Owner',true) ON CONFLICT(email) DO UPDATE SET password_hash=excluded.password_hash`, user, string(hash))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO memberships(tenant_id,user_id,role) VALUES($1,$2,'owner') ON CONFLICT DO NOTHING`, org, user)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO plans(id,code,name,currency,monthly_price_minor,included_units,overage_price_minor) VALUES($1,'starter','Starter','IDR',99000,10000,15),($2,'pro','Pro','IDR',299000,100000,10) ON CONFLICT(code) DO UPDATE SET name=excluded.name`, starter, pro)
	if err != nil {
		return err
	}
	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.AddDate(0, 1, 0)
	_, err = tx.Exec(ctx, `INSERT INTO subscriptions(id,tenant_id,plan_id,status,current_period_start,current_period_end) VALUES($1,$2,$3,'active',$4,$5) ON CONFLICT DO NOTHING`, "40000000-0000-4000-8000-000000000001", org, starter, start, end)
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Println("seeded owner@acme.test / demo-password (tenant: acme)")
	return nil
}
