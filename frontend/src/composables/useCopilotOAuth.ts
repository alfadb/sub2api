import { ref, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'

export function useCopilotOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()

  const loading = ref(false)
  const polling = ref(false)
  const deviceCode = ref('')
  const userCode = ref('')
  const verificationUri = ref('')
  const interval = ref(5)
  const expiresIn = ref(0)
  const error = ref('')

  let pollTimer: ReturnType<typeof setTimeout> | null = null

  const resetState = () => {
    stopPolling()
    loading.value = false
    polling.value = false
    deviceCode.value = ''
    userCode.value = ''
    verificationUri.value = ''
    interval.value = 5
    expiresIn.value = 0
    error.value = ''
  }

  const startDeviceCodeFlow = async (): Promise<boolean> => {
    loading.value = true
    error.value = ''

    try {
      const response = await adminAPI.copilot.initiateDeviceCode()
      deviceCode.value = response.device_code
      userCode.value = response.user_code
      verificationUri.value = response.verification_uri
      interval.value = response.interval || 5
      expiresIn.value = response.expires_in || 900
      return true
    } catch (err: any) {
      error.value =
        err.response?.data?.detail || t('admin.accounts.oauth.copilot.failedToStartFlow')
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  const startPolling = (onSuccess: (accessToken: string) => void) => {
    if (!deviceCode.value) {
      error.value = 'No device code available'
      return
    }

    polling.value = true
    error.value = ''
    const startTime = Date.now()
    const maxDuration = expiresIn.value * 1000

    const poll = async () => {
      if (!polling.value) return

      // Check expiration
      if (Date.now() - startTime > maxDuration) {
        polling.value = false
        error.value = t('admin.accounts.oauth.copilot.codeExpired')
        return
      }

      try {
        const response = await adminAPI.copilot.pollToken(deviceCode.value)

        switch (response.status) {
          case 'success':
            polling.value = false
            if (response.access_token) {
              onSuccess(response.access_token)
            }
            return

          case 'slow_down':
            // Increase interval by 5 seconds as per GitHub spec
            interval.value = Math.min(interval.value + 5, 30)
            break

          case 'expired':
            polling.value = false
            error.value = t('admin.accounts.oauth.copilot.codeExpired')
            return

          case 'error':
            polling.value = false
            error.value = response.error || t('admin.accounts.oauth.copilot.authFailed')
            return

          case 'pending':
          default:
            // Continue polling
            break
        }
      } catch (err: any) {
        // Network errors - continue polling unless it's a definitive failure
        if (err.response?.status >= 400 && err.response?.status < 500 && err.response?.status !== 428) {
          polling.value = false
          error.value = err.response?.data?.detail || t('admin.accounts.oauth.copilot.authFailed')
          return
        }
      }

      if (polling.value) {
        pollTimer = setTimeout(poll, interval.value * 1000)
      }
    }

    pollTimer = setTimeout(poll, interval.value * 1000)
  }

  const stopPolling = () => {
    polling.value = false
    if (pollTimer) {
      clearTimeout(pollTimer)
      pollTimer = null
    }
  }

  onUnmounted(() => {
    stopPolling()
  })

  return {
    loading,
    polling,
    deviceCode,
    userCode,
    verificationUri,
    interval,
    expiresIn,
    error,
    startDeviceCodeFlow,
    startPolling,
    stopPolling,
    resetState
  }
}