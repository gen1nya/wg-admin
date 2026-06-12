import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router';
import { api } from './api/client';

const routes: RouteRecordRaw[] = [
  { path: '/login', name: 'login', component: () => import('./pages/Login.vue'), meta: { public: true } },
  { path: '/', name: 'overview', component: () => import('./pages/Overview.vue') },
  {
    path: '/interfaces/:name',
    name: 'interface',
    component: () => import('./pages/Interface.vue'),
    props: true,
  },
  { path: '/map', name: 'map', component: () => import('./pages/Map.vue') },
  { path: '/plans', name: 'plans', component: () => import('./pages/Plans.vue') },
  { path: '/plans/new', name: 'plan-new', component: () => import('./pages/PlanNew.vue') },
  {
    path: '/plans/:id',
    name: 'plan',
    component: () => import('./pages/PlanDetail.vue'),
    props: (route) => ({ id: Number(route.params.id) }),
  },
  { path: '/audit', name: 'audit', component: () => import('./pages/Audit.vue') },
  { path: '/:pathMatch(.*)*', redirect: '/' },
];

export const router = createRouter({
  history: createWebHistory(),
  routes,
});

let authChecked = false;
let authenticated = false;

async function ensureAuth(): Promise<boolean> {
  if (authChecked) return authenticated;
  try {
    await api.whoami();
    authenticated = true;
  } catch {
    authenticated = false;
  }
  authChecked = true;
  return authenticated;
}

export function invalidateAuth(): void {
  authChecked = false;
  authenticated = false;
}

export function markAuthenticated(): void {
  authChecked = true;
  authenticated = true;
}

router.beforeEach(async (to) => {
  if (to.meta.public) return true;
  const ok = await ensureAuth();
  if (!ok) return { name: 'login', query: { next: to.fullPath } };
  return true;
});
