import { createRouter, createWebHistory } from 'vue-router'
import Dashboard from '../views/Dashboard.vue'
import Sections from '../views/Sections.vue'
import SectionDetail from '../views/SectionDetail.vue'
import SectionHealthDetail from '../views/SectionHealthDetail.vue'
import Alerts from '../views/Alerts.vue'

const routes = [
  { path: '/', name: 'Dashboard', component: Dashboard },
  { path: '/sections', name: 'Sections', component: Sections },
  { path: '/sections/:id', name: 'SectionDetail', component: SectionDetail },
  { path: '/sections/:id/health', name: 'SectionHealthDetail', component: SectionHealthDetail },
  { path: '/alerts', name: 'Alerts', component: Alerts },
]

export default createRouter({
  history: createWebHistory(),
  routes,
})