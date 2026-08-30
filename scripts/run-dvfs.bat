@echo off
setlocal enabledelayedexpansion

set NUM_CLIENTS=%1
if "%NUM_CLIENTS%"=="" set NUM_CLIENTS=3

echo Building Docker image...
docker compose build

echo Starting DVFS Server and %NUM_CLIENTS% Clients...
docker compose up -d --scale client=%NUM_CLIENTS%

echo Waiting for containers to initialize...
timeout /t 3 /nobreak > nul

echo Opening terminals...

start "DVFS Server Logs" cmd /k "docker compose logs -f server"

for /f "tokens=*" %%i in ('docker compose ps client --format "{{.Name}}"') do (
    echo Attaching to client: %%i
    start "DVFS Client - %%i" cmd /k "docker exec -it %%i /bin/sh -c "make exec-client USER=user_%%i IP_ADDR=server""
)

echo All windows opened.
pause
