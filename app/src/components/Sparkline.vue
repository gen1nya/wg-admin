<script setup lang="ts">
import { computed } from 'vue';

const props = withDefaults(
  defineProps<{
    values: number[];
    color?: string;
    height?: number;
    width?: number;
    opacity?: number;
    fill?: boolean;              // true → заполняем область под линией
    mirror?: boolean;            // true → рисуем снизу вверх (для tx в зеркальной раскладке)
    minMax?: number;             // внешний максимум (для совмещённых tx/rx на одной шкале)
  }>(),
  {
    color: '#10b981',
    height: 24,
    width: 100,
    opacity: 1,
    fill: true,
    mirror: false,
    minMax: 0,
  },
);

const path = computed(() => {
  const n = props.values.length;
  if (n === 0) return '';
  const localMax = Math.max(...props.values, 1);
  const max = Math.max(props.minMax, localMax);
  const stepX = props.width / Math.max(1, n - 1);
  const h = props.height;

  let d = '';
  for (let i = 0; i < n; i++) {
    const x = i * stepX;
    const rel = props.values[i]! / max;
    const y = props.mirror ? rel * h : h - rel * h;
    d += (i === 0 ? 'M' : 'L') + x.toFixed(2) + ',' + y.toFixed(2) + ' ';
  }
  if (props.fill) {
    // закрываем контур к базовой оси для fill
    const lastX = (n - 1) * stepX;
    const baseY = props.mirror ? 0 : h;
    d += `L${lastX.toFixed(2)},${baseY} L0,${baseY} Z`;
  }
  return d;
});
</script>

<template>
  <!--
    width как атрибут НЕ задаём: SVG тогда растягивается на 100% контейнера.
    Проп `width` используется только как размер координатной системы
    (viewBox), но рендер масштабируется до реальной ширины wrapper'а.
    Высоту задаём в style — иначе inline-SVG добавит baseline-strut под низом.
  -->
  <svg
    :viewBox="`0 0 ${width} ${height}`"
    preserveAspectRatio="none"
    :style="{ opacity, display: 'block', width: '100%', height: height + 'px' }"
  >
    <path
      :d="path"
      :fill="fill ? color : 'none'"
      :stroke="color"
      stroke-width="1"
      :fill-opacity="fill ? 0.3 : 0"
    />
  </svg>
</template>
