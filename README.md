<p align="center">
  <img src="https://raw.githubusercontent.com/filebrowser/filebrowser/master/branding/banner.png" width="550"/>
</p>

[![Build](https://github.com/filebrowser/filebrowser/actions/workflows/ci.yaml/badge.svg)](https://github.com/filebrowser/filebrowser/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/filebrowser/filebrowser/v2)](https://goreportcard.com/report/github.com/filebrowser/filebrowser/v2)
[![Version](https://img.shields.io/github/release/filebrowser/filebrowser.svg)](https://github.com/filebrowser/filebrowser/releases/latest)

File Browser provides a file managing interface within a specified directory and it can be used to upload, delete, preview and edit your files. It is a **create-your-own-cloud**-kind of software where you can just install it on your server, direct it to a path and access your files through a nice web interface.

## Fork 改动说明

本 Fork 基于 [filebrowser/filebrowser](https://github.com/filebrowser/filebrowser) 进行了以下修改：

### 1. 默认语言改为简体中文
- 新用户默认语言设置为 `zh-cn`（简体中文）
- 支持通过启动参数 `--defaults.locale` 自定义默认语言

```bash
# 使用简体中文（默认）
./filebrowser

# 使用英文
./filebrowser --defaults.locale=en

# 使用繁体中文
./filebrowser --defaults.locale=zh-tw
```

### 2. 流式解压功能
新增在线解压功能，支持以下格式：
- `.zip`
- `.tar.gz` / `.tgz`
- `.tar`

**特性：**
- **低内存占用**：使用 8KB 流式缓冲区，无论压缩包多大，内存占用约 50-100KB
- **安全防护**：防止 zip slip 路径穿越攻击
- **使用方式**：右键点击压缩文件，选择"解压"即可

**API 接口：**
```
POST /api/extract/{path}?destination={optional_dest}
```

### Docker 镜像

```bash
docker pull ghcr.io/fy0/filebrowser:latest
```

## Documentation

Documentation on how to install, configure, and contribute to this project is hosted at [filebrowser.org](https://filebrowser.org).

## Project Status

This project is a finished product which fulfills its goal: be a single binary web File Browser which can be run by anyone anywhere. That means that File Browser is currently on **maintenance-only** mode. Therefore, please note the following:

- It can take a while until someone gets back to you. Please be patient.
- [Issues](https://github.com/filebrowser/filebrowser/issues) are meant to track bugs. Unrelated issues will be converted into [discussions](https://github.com/filebrowser/filebrowser/discussions).
- No new features will be implemented by maintainers. Pull requests for new features will be reviewed on a case by case basis.
- The priority is triaging issues, addressing security issues and reviewing pull requests meant to solve bugs.

## Contributing

Contributions are always welcome. To start contributing to this project, read our [guidelines](CONTRIBUTING.md) first.

## License

[Apache License 2.0](LICENSE) © File Browser Contributors
