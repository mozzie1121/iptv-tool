import axios from 'axios'
import { ElMessage } from 'element-plus'
import i18n from '../i18n'

const { t } = i18n.global

const api = axios.create({
  baseURL: '/api',
  timeout: 30000,
})

// Request interceptor: attach JWT token and language header
api.interceptors.request.use(config => {
  const token = localStorage.getItem('token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  config.headers['X-Language'] = localStorage.getItem('locale') || 'en'
  return config
})

// Response interceptor: handle errors globally
api.interceptors.response.use(
  response => response,
  error => {
    if (error.response) {
      const { status, data } = error.response
      const isLoginRequest = error.config && error.config.url === '/login' && error.config.method === 'post'

      if (status === 429) {
        if (!isLoginRequest) {
          ElMessage.error((data && data.error) || t('api_interceptor.rate_limited'))
        }
      } else if (status === 403 && isLoginRequest) {
        // Captcha-related 403 handled by Login.vue
      } else if (status === 401) {
        if (isLoginRequest) {
          // Login 401 handled by Login.vue
        } else {
          localStorage.removeItem('token')
          window.location.hash = '#/login'
          ElMessage.error(t('api_interceptor.session_expired'))
        }
      } else if (data && data.error) {
        ElMessage.error(data.error)
      }
    } else {
      ElMessage.error(t('api_interceptor.network_error'))
    }
    return Promise.reject(error)
  }
)

export default api
