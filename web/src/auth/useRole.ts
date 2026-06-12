import { useAuth } from '@/auth/AuthContext'

// Role helpers read the current user's global_role from the auth context.
// The backend is the source of truth (admin endpoints re-check the role); these
// only drive what the UI shows/enables. Mutations on admin surfaces require
// admin; reads are open to admin+pi ("privileged").

export function useIsAdmin(): boolean {
  const { user } = useAuth()
  return user?.global_role === 'admin'
}

export function useIsPrivileged(): boolean {
  const { user } = useAuth()
  return user?.global_role === 'admin' || user?.global_role === 'pi'
}
