package fileserver

import pb "github.com/umangshikarvar/dvfs/api/fileserver"

// checkACL verifies if user has permission for an operation
func (fs *FileServer) checkACL(acl *pb.ACL, user string, operation string) bool {
	if acl == nil {
		return false
	}

	var allowedUsers []string
	switch operation {
	case "read":
		allowedUsers = acl.ReadUsers
	case "write":
		allowedUsers = acl.WriteUsers
	default:
		return false
	}

	for _, u := range allowedUsers {
		if u == "*" || u == user {
			return true
		}
	}
	return false
}
