import {
  createRouter,
  createRootRoute,
  createRoute,
  Outlet,
  redirect,
} from '@tanstack/react-router'
import { LoginPage } from '@/pages/LoginPage'
import { RunsPage } from '@/pages/RunsPage'
import { RunDetailPage } from '@/pages/RunDetailPage'
import { ReviewsPage } from '@/pages/ReviewsPage'
import { ReviewDetailPage } from '@/pages/ReviewDetailPage'
import { CatalogPage } from '@/pages/CatalogPage'
import { SampleDetailPage } from '@/pages/SampleDetailPage'
import { DeviceDetailPage } from '@/pages/DeviceDetailPage'
import { JupyterPage } from '@/pages/JupyterPage'
import { AdminPage } from '@/pages/AdminPage'
import { AppShell } from '@/components/AppShell'

// Auth guard accessor — injected by main.tsx after auth resolves
let isAuthenticated: () => boolean = () => false
export function setIsAuthenticated(fn: () => boolean) {
  isAuthenticated = fn
}

// Privileged (admin or pi) accessor — gates the /admin route. Injected by
// main.tsx from the current user's global_role.
let isPrivileged: () => boolean = () => false
export function setIsPrivileged(fn: () => boolean) {
  isPrivileged = fn
}

// ─── Root ─────────────────────────────────────────────────────────────────────

const rootRoute = createRootRoute({
  component: () => <Outlet />,
})

// ─── Public routes ────────────────────────────────────────────────────────────

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
})

// ─── Auth guard ───────────────────────────────────────────────────────────────

const authGuardRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'auth',
  beforeLoad: () => {
    if (!isAuthenticated()) {
      throw redirect({ to: '/login' })
    }
  },
  component: AppShell,
})

// ─── Protected routes ─────────────────────────────────────────────────────────

const runsRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/',
  component: RunsPage,
})

const runDetailRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/runs/$id',
  component: RunDetailPage,
})

const reviewsRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/reviews',
  component: ReviewsPage,
})

const reviewDetailRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/reviews/$runId',
  component: ReviewDetailPage,
})

const catalogRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/catalog',
  component: CatalogPage,
})

const sampleDetailRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/catalog/samples/$id',
  component: SampleDetailPage,
})

const deviceDetailRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/catalog/devices/$id',
  component: DeviceDetailPage,
})

const jupyterRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/jupyter',
  component: JupyterPage,
})

// Admin is gated on the privileged (admin|pi) role in addition to auth. The
// backend re-checks the role on every admin endpoint; this only keeps the page
// out of reach for regular members.
const adminRoute = createRoute({
  getParentRoute: () => authGuardRoute,
  path: '/admin',
  beforeLoad: () => {
    if (!isPrivileged()) {
      throw redirect({ to: '/' })
    }
  },
  component: AdminPage,
})

// ─── Router ───────────────────────────────────────────────────────────────────

const routeTree = rootRoute.addChildren([
  loginRoute,
  authGuardRoute.addChildren([
    runsRoute,
    runDetailRoute,
    reviewsRoute,
    reviewDetailRoute,
    catalogRoute,
    sampleDetailRoute,
    deviceDetailRoute,
    jupyterRoute,
    adminRoute,
  ]),
])

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
})

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
