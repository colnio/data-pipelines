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
import { AppShell } from '@/components/AppShell'

// Auth guard accessor — injected by main.tsx after auth resolves
let isAuthenticated: () => boolean = () => false
export function setIsAuthenticated(fn: () => boolean) {
  isAuthenticated = fn
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

// ─── Router ───────────────────────────────────────────────────────────────────

const routeTree = rootRoute.addChildren([
  loginRoute,
  authGuardRoute.addChildren([
    runsRoute,
    runDetailRoute,
    reviewsRoute,
    reviewDetailRoute,
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
