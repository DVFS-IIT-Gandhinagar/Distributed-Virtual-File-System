#!/bin/bash

sudo systemctl enable --now ssh 

sudo systemctl mask sleep.target suspend.target hibernate.target hybrid-sleep.target
