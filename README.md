# farc

Система для архивирования видео/аудио потоков на диск с использованием fblocks (блоков фиксированного размера) и раздачи архива обратно по HTTP/HLS. Сам `farc`/`farcd` — это демон архивации; `hls_server` и SPA в `web/` строятся поверх него (см. ниже).

## Компоненты

- **`farc`** (`cmd/farc`, запускается как демон `farcd`) — принимает RTSP-потоки и записывает их на диск в fblocks; предоставляет HTTP+WebSocket API для чтения архива обратно (`internal/api`).
- **`hls_server`** (`cmd/hls_server`) — отдельный процесс, превращающий архив `farcd` в HLS, воспроизводимый в браузере (H.264/AAC, только VOD, CMAF-ремукс, без перекодирования). Общается с `farcd` только через его внешний API.
- **`web/`** — SPA на React: консоль администратора для `farcd` (хранилища, политика захвата) плюс VOD-плеер на основе `hls_server`.

Проектная документация (в `docs/docs/archive/`) описывает полную архитектуру fblock/storage; `PLAN.md` отслеживает текущий план реализации и известные пробелы для веб-клиента и развёртывания.

## Сборка

Требуется Go 1.21+. С помощью [Task](https://taskfile.dev):

```sh
task build          # собирает farc, hls_server и web/dist
task build/app       # только farc
task build/hls_server
task build/web       # требует Node.js/npm
```

Или напрямую:

```sh
go build -o farc ./cmd/farc
go build -o hls_server ./cmd/hls_server
```

`task test` / `go test ./...` запускают набор юнит-тестов. В `tests/` находится end-to-end тест (build tag `e2e`), запускающий оба бинарника как настоящие процессы ОС:

```sh
go test -tags e2e ./tests/... -v
```

`task lint` запускает `golangci-lint`. **Не доверяйте остальному содержимому `taskfile.yaml`** — несколько задач (`run`, `help`, `db/*`, `env/*`) скопированы из другого, не связанного с этим проекта и здесь не применимы.

## Запуск

Оба бинарника принимают флаг `-c`/`--config`, указывающий на JSON-файл конфигурации (`internal/config`, `internal/hlsconfig`):

```sh
./farc -c farc.config.json
./hls_server -c hls_server.config.json
```

Адреса HTTP/WS/Metrics-серверов `farc` берутся из окружения, а не из JSON-файла — `FARC_HTTP_IP`/`FARC_HTTP_PORT`, `FARC_WS_IP`/`FARC_WS_PORT`/`FARC_WS_MAX_CONNECTIONS`, `FARC_METRICS_IP`/`FARC_METRICS_PORT` (`*_IP` по умолчанию `0.0.0.0`, `*_PORT` обязателен). `FARC_LOG_DIR` — необязательная директория, куда `farcd` дополнительно (вместе со stderr) пишет `farcd.log`; если не задана — только stderr, как раньше. HTTP/WS-серверы `farcd` логируют по одной access-log строке на запрос через `X-Request-Id`/`X-Session-Id` — если envoy (или любой другой прокси перед системой) проставляет эти заголовки, они попадают в лог этой строкой (`request_id=...`/`session_id=...`); заголовки не проставляются, если их нет во входящем запросе. — благодаря этому окружение рабочего развёртывания (или файл `.env` рядом с бинарником, загружаемый через `godotenv`) можно закоммитить вместе с `docker-compose.yaml`, не раскрывая топологию конкретной площадки в `farc.config.json`. Сам `farc.config.json` больше вообще не поставляется как файл в репозитории: если `-c` указывает на путь, который ещё не существует, `farc` создаёт пустой файл (`internal/config.EnsureExists`) вместо того, чтобы завершиться с ошибкой — так что свежий, пустой конфиг-файл/volume просто работает. Его список `storages` должен ссылаться на уже инициализированные файлы-образы хранилищ — `farcd` никогда не создаёт их сам (оператору нужно лишь заранее выделить размер партиции/файла; `farcd` инициализирует его через `POST /storages`, например через страницу Storages веб-клиента). `farcd` сохраняет вновь созданное хранилище обратно в `farc.config.json`, так что оно снова подхватывается при следующем перезапуске — конфиг-файл должен быть доступен процессу на запись (см. сервис `farc` в `docker-compose.yaml`, который держит его именно поэтому на отдельном volume `farc_config`).

HTTP-адрес `hls_server`, тот единственный farcd, с которым он общается (ADR-020 — v1 поддерживает ровно один, а не список), и настройки сегментов/кэша задаются через окружение так же — `HLS_SERVER_HTTP_IP`/`HLS_SERVER_HTTP_PORT`, `HLS_SERVER_FARC_HTTP`/`HLS_SERVER_FARC_WS`, `HLS_SERVER_TARGET_SEGMENT_DURATION`, `HLS_SERVER_CACHE_DIR`, `HLS_SERVER_CACHE_QUOTA_BYTES` (`*_PORT`, `*_FARC_HTTP`, `*_FARC_WS`, `*_TARGET_SEGMENT_DURATION`, `*_CACHE_DIR` обязательны; `*_CACHE_QUOTA_BYTES` <= 0 означает без ограничения). `HLS_SERVER_LOG_DIR` — необязательная директория для `hls_server.log`, тот же смысл, что и `FARC_LOG_DIR` у `farcd`. `hls_server` так же логирует по `X-Request-Id`/`X-Session-Id` на входе и прокидывает их дальше в свои запросы к `farcd`, так что один и тот же запрос браузера виден под одним и тем же `request_id` в логах обоих сервисов. `hls_server.config.json` хранит только соответствие канал → id хранилища на стороне farcd. Он тоже больше не поставляется как файл в репозитории: как и `farc.config.json`, если `-c` указывает на путь, который ещё не существует, `hls_server` создаёт пустой файл (`internal/hlsconfig.EnsureExists`) вместо ошибки — но, в отличие от `farc`, `hls_server` никогда сам не перезаписывает этот файл впоследствии (CRUD API для каналов здесь нет), так что расширение его сверх пустого — по-прежнему ручной шаг оператора.

## Docker / полный стек

```sh
docker compose up --build
```

Поднимает `farc`, `hls_server` и `web` (nginx, раздающий SPA и проксирующий `/api/farcd/` и `/api/hls/` на два backend'а) на `http://localhost/`. `farc.config.json` живёт на volume `farc_config` (при первом запуске создаётся пустым, затем растёт через страницы Storages/Channels веб-клиента); `hls_server.config.json` живёт на своём собственном volume `hls_config` так же, хотя после этого ничто не растит его автоматически — канал, добавленный в `farc.config.json`, всё ещё требует добавления соответствующей записи в `hls_server.config.json` вручную (нерешённый пока вопрос синхронизации из §8 `docs/docs/archive/12-hls-server.md`); данные архива/кэша живут в именованных volume `farc_data`/`hls_cache`.

## Документация

Проектная документация собирается с помощью [mkdocs-material](https://squidfunk.github.io/mkdocs-material/):

```sh
mkdocs serve -f docs/mkdocs.yaml
```
