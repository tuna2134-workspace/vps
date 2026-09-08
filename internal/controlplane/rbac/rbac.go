// Package rbac centralizes role-based access control decisions so permission
// checks do not spread across HTTP handlers.
package rbac

import (
	"github.com/tuna2134/vps/internal/controlplane/models"
)

// roleRank assigns an ordering to roles for hierarchical checks.
var roleRank = map[models.Role]int{
	models.RoleUser:   1,
	models.RoleSupport: 2,
	models.RoleAdmin:  3,
}

// HasRole reports whether the principal holds exactly the given role.
func HasRole(roles []models.Role, target models.Role) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}

// HasAnyRole reports whether the principal holds at least one of the targets.
func HasAnyRole(roles []models.Role, targets ...models.Role) bool {
	for _, r := range roles {
		for _, t := range targets {
			if r == t {
				return true
			}
		}
	}
	return false
}

// Require reports whether the principal's highest role meets or exceeds the
// required role.
func Require(roles []models.Role, required models.Role) bool {
	highest := 0
	for _, r := range roles {
		if rank, ok := roleRank[r]; ok && rank > highest {
			highest = rank
		}
	}
	return highest >= roleRank[required]
}

// IsAdmin is a convenience predicate.
func IsAdmin(roles []models.Role) bool {
	return HasRole(roles, models.RoleAdmin)
}

// IsSupportOrAdmin is a convenience predicate.
func IsSupportOrAdmin(roles []models.Role) bool {
	return HasAnyRole(roles, models.RoleSupport, models.RoleAdmin)
}