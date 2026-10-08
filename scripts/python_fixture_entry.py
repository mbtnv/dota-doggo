"""Test adapter: redirect aiogram to the fixture API and match the Go poll interval.

Loaded only by compare_runtime.py into the unchanged Python reference image.
The original application, repositories, jobs and handlers run normally.
"""

import os
import runpy
import sys

from aiogram import Bot
from aiogram.client.session.aiohttp import AiohttpSession
from aiogram.client.telegram import TelegramAPIServer
import dota_dog.infra.telegram.bot as bot_module
import dota_dog.settings as settings_module


def fixture_bot(settings):
    session = AiohttpSession(api=TelegramAPIServer.from_base(os.environ["TELEGRAM_BASE_URL"]))
    return Bot(token=settings.bot_token, session=session)


original_settings = settings_module.load_settings


def fixture_settings():
    settings = original_settings()
    # The reference validates integer minutes; only the test clock is accelerated.
    return settings.model_copy(update={"poll_interval_minutes": 0.01})


bot_module.create_bot = fixture_bot
settings_module.load_settings = fixture_settings
runpy.run_module("dota_dog." + ("app" if sys.argv[1] == "bot" else "worker"), run_name="__main__")
