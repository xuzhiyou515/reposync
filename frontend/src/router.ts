import { createRouter, createWebHistory } from 'vue-router'
import DashboardPage from './views/DashboardPage.vue'

export default createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      component: DashboardPage,
    },
  ],
})
