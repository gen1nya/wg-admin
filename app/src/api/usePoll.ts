// usePoll — простой композабл-обёртка над setInterval, который автоматически
// подчищает таймер при unmount. Фетч-функция вызывается сразу при mount и
// дальше каждые intervalMs миллисекунд. Несколько компонентов могут
// независимо поллить разные вещи — этот модуль не делает global state.
//
// Если станет важно схлопнуть многочисленные подписки в один запрос —
// переедем на общий store с одним setInterval на всё приложение.

import { onMounted, onUnmounted, ref } from 'vue';

export function usePoll<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
): { data: ReturnType<typeof ref<T | null>>; error: ReturnType<typeof ref<string | null>>; refresh: () => Promise<void> } {
  const data = ref<T | null>(null) as ReturnType<typeof ref<T | null>>;
  const error = ref<string | null>(null);
  let timer: ReturnType<typeof setInterval> | null = null;

  async function refresh() {
    try {
      data.value = await fetcher();
      error.value = null;
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
    }
  }

  onMounted(() => {
    refresh();
    timer = setInterval(refresh, intervalMs);
  });
  onUnmounted(() => {
    if (timer) clearInterval(timer);
    timer = null;
  });

  return { data, error, refresh };
}
