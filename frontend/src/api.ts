import axios from 'axios'
import type { Credential, ExecutionDetail, RepoCache, SyncExecution, SyncTask, WebhookEvent } from './types'

const http = axios.create({
  baseURL: '/api',
})

export const api = {
  executionStreamUrl: (id: number) => `/api/executions/${id}/stream`,
  executionWebSocketUrl: (id: number) => {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${protocol}//${window.location.host}/api/executions/${id}/ws`
  },
  listTasks: async () => (await http.get<SyncTask[]>('/tasks')).data,
  saveTask: async (task: Partial<SyncTask>) => {
    if (task.id) {
      return (await http.put<SyncTask>(`/tasks/${task.id}`, task)).data
    }
    return (await http.post<SyncTask>('/tasks', task)).data
  },
  deleteTask: async (id: number) => {
    await http.delete(`/tasks/${id}`)
  },
  runTask: async (id: number) => (await http.post<SyncExecution>(`/tasks/${id}/run`)).data,
  listExecutions: async (id: number) => (await http.get<SyncExecution[]>(`/tasks/${id}/executions`)).data,
  listWebhookEvents: async (id: number) => (await http.get<WebhookEvent[]>(`/tasks/${id}/webhook-events`)).data,
  replayWebhookEvent: async (taskId: number, eventId: number) =>
    (await http.post<SyncExecution>(`/tasks/${taskId}/webhook-events/${eventId}/replay`)).data,
  executionDetail: async (id: number) => (await http.get<ExecutionDetail>(`/executions/${id}`)).data,
  listCredentials: async () => (await http.get<Credential[]>('/credentials')).data,
  saveCredential: async (credential: Partial<Credential>) => {
    if (credential.id) {
      return (await http.put<Credential>(`/credentials/${credential.id}`, credential)).data
    }
    return (await http.post<Credential>('/credentials', credential)).data
  },
  deleteCredential: async (id: number) => {
    await http.delete(`/credentials/${id}`)
  },
  listCaches: async () => (await http.get<RepoCache[]>('/caches')).data,
  cleanupCache: async (id: number) => {
    await http.post(`/caches/${id}/cleanup`)
  },
}
