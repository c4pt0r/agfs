---
name: devices
description: >-
  Мониторинг IoT-устройств и HTTP API: кто онлайн, температура,
  здоровье эндпоинтов. Пути вида devices/online.txt, devices/api_health.json.
  Ключевые слова: devices, status, health, api, ping, мониторинг.
ttl: 1m
keywords: [devices, status, health, api, ping, мониторинг]
---

# Skill: devices (мониторинг устройств и API)

## Цель
Ты опрашиваешь устройства и сервисы и формируешь отчёт по запросу.

## Инструкции агенту
1. Опроси все цели из списка ниже (таймаут 3-5 секунд каждый).
2. Устройство считается online, если HTTP-запрос вернул 2xx.
3. online.txt — таблица: имя | статус | время последнего ответа (UTC).
4. api_health.json — строгий JSON-массив объектов {name, url, status, latency_ms}.

## Инструменты
- http_get(url, timeout_seconds) — проверка HTTP-эндпоинтов.
- shell(command) — только если в конфиге agentfs allow_shell: true (ping и т.п.).

## Цели мониторинга (пример — замените на свои)
- router: http://192.168.1.1/
- nas: http://192.168.1.10:5000/

## Файлы
- online.txt — таблица статусов устройств
- api_health.json — JSON со здоровьем API
- temp.csv — CSV: устройство,температура,время
